package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/timisah/101conv/server/internal/profile"
)

const maxCombinedTargetSizeMB int64 = int64(MaxBatchItems) * (MaxOutputBytes / (1 << 20))

type compressionBudgetItem struct {
	SizeBytes int64
	MinMB     int64
	MaxMB     int64
	TargetKey string
}

// allocateCompressionBudget uses a constrained proportional allocation. The
// unconstrained case is proportional to input bytes; profile minima and maxima
// are fixed first and the remaining budget is redistributed among the other
// files. A combined target is a maximum, so a profile whose total caps are
// below the requested budget is valid and simply uses less than the maximum.
func allocateCompressionBudget(items []compressionBudgetItem, budgetMB int64) ([]int64, error) {
	if len(items) == 0 || budgetMB < 1 {
		return nil, fmt.Errorf("%w: combined target size must be positive", ErrInvalidInput)
	}
	allocations := make([]int64, len(items))
	active := make([]bool, len(items))
	remaining := budgetMB
	var minimumTotal int64
	for i, item := range items {
		if item.SizeBytes < 1 || item.MinMB < 1 || item.MaxMB < item.MinMB {
			return nil, fmt.Errorf("%w: combined target has invalid item bounds", ErrInvalidInput)
		}
		active[i] = true
		if minimumTotal > math.MaxInt64-item.MinMB {
			return nil, fmt.Errorf("%w: combined target minimums overflow", ErrInvalidInput)
		}
		minimumTotal += item.MinMB
	}
	if remaining < minimumTotal {
		return nil, fmt.Errorf("%w: combined target is smaller than the minimum output allocation", ErrInvalidInput)
	}

	for {
		var totalWeight int64
		activeCount := 0
		for i, item := range items {
			if active[i] {
				activeCount++
				if totalWeight > math.MaxInt64-item.SizeBytes {
					return nil, fmt.Errorf("%w: combined target input sizes overflow", ErrInvalidInput)
				}
				totalWeight += item.SizeBytes
			}
		}
		if activeCount == 0 || totalWeight < 1 || remaining < 1 {
			break
		}

		fixed := false
		for i, item := range items {
			if !active[i] {
				continue
			}
			share := float64(remaining) * float64(item.SizeBytes) / float64(totalWeight)
			if share < float64(item.MinMB)-1e-9 {
				allocations[i] = item.MinMB
				remaining -= item.MinMB
				active[i] = false
				fixed = true
			} else if share > float64(item.MaxMB)+1e-9 {
				allocations[i] = item.MaxMB
				remaining -= item.MaxMB
				active[i] = false
				fixed = true
			}
		}
		if fixed {
			continue
		}

		// All active items are within their bounds. Largest-remainder rounding
		// preserves the requested maximum while keeping proportional shares.
		type remainder struct {
			index int
			value float64
		}
		remainders := make([]remainder, 0, activeCount)
		var assigned int64
		for i, item := range items {
			if !active[i] {
				continue
			}
			share := float64(remaining) * float64(item.SizeBytes) / float64(totalWeight)
			whole := int64(math.Floor(share))
			if whole < item.MinMB {
				whole = item.MinMB
			}
			if whole > item.MaxMB {
				whole = item.MaxMB
			}
			allocations[i] = whole
			assigned += whole
			remainders = append(remainders, remainder{index: i, value: share - math.Floor(share)})
		}
		left := remaining - assigned
		sort.SliceStable(remainders, func(i, j int) bool {
			if math.Abs(remainders[i].value-remainders[j].value) < 1e-9 {
				return remainders[i].index < remainders[j].index
			}
			return remainders[i].value > remainders[j].value
		})
		for left > 0 {
			progress := false
			for _, candidate := range remainders {
				if left == 0 {
					break
				}
				i := candidate.index
				if allocations[i] >= items[i].MaxMB {
					continue
				}
				allocations[i]++
				left--
				progress = true
			}
			if !progress {
				break
			}
		}
		remaining = left
		break
	}

	for i, item := range items {
		if allocations[i] < item.MinMB || allocations[i] > item.MaxMB {
			return nil, fmt.Errorf("%w: combined target allocation is outside the profile bounds", ErrInvalidInput)
		}
	}
	return allocations, nil
}

func (s *Service) applyCombinedCompressionBudget(ctx context.Context, req *BatchRequest, ownerID string) error {
	if req.CombinedTargetSizeMB == 0 {
		return nil
	}
	items := make([]compressionBudgetItem, len(req.Items))
	for i := range req.Items {
		item := &req.Items[i]
		var source string
		var size int64
		err := s.db.QueryRowContext(ctx, `SELECT extension,size_bytes FROM conv_uploads WHERE id=$1 AND owner_id=$2`, item.UploadID, ownerID).Scan(&source, &size)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: item %d upload is unavailable", ErrNotFound, i+1)
		}
		if err != nil {
			return err
		}

		profileID := strings.TrimSpace(item.ProfileID)
		if profileID == "" {
			var resolveErr error
			profileID, _, resolveErr = s.ResolvePreferredProfile(ctx, ownerID, "compress", source, item.TargetFormat)
			if resolveErr != nil {
				return resolveErr
			}
		}
		definition, rule, _, _, resolveErr := s.management.ResolveConversion(profileID, source, item.TargetFormat)
		if resolveErr != nil {
			if strings.Contains(resolveErr.Error(), "does not support") {
				return fmt.Errorf("%w: %v", ErrUnsupported, resolveErr)
			}
			return resolveErr
		}
		if !definition.SupportsMode("compress") || definition.Compression == nil || len(rule.CompressionArguments) == 0 {
			return fmt.Errorf("%w: profile does not provide compression for %s to %s", ErrUnsupported, source, item.TargetFormat)
		}
		option, err := combinedTargetOption(definition.CompressionForRule(rule))
		if err != nil {
			return err
		}
		minMB, maxMB, err := wholeMBBounds(option)
		if err != nil {
			return err
		}
		items[i] = compressionBudgetItem{SizeBytes: size, MinMB: minMB, MaxMB: maxMB, TargetKey: option.Key}
		item.ProfileID = profileID
		item.Options = copyOptions(item.Options)
	}

	allocations, err := allocateCompressionBudget(items, req.CombinedTargetSizeMB)
	if err != nil {
		return err
	}
	for i := range req.Items {
		req.Items[i].Options[items[i].TargetKey] = strconv.FormatInt(allocations[i], 10)
	}
	return nil
}

func combinedTargetOption(config profile.CompressionConfig) (profile.OptionDefinition, error) {
	var found profile.OptionDefinition
	count := 0
	for _, option := range config.Options {
		if option.TargetSizeMB {
			found = option
			count++
		}
	}
	if count != 1 {
		return profile.OptionDefinition{}, fmt.Errorf("%w: combined compression budgets require exactly one target-size option", ErrUnsupported)
	}
	return found, nil
}

func wholeMBBounds(option profile.OptionDefinition) (int64, int64, error) {
	if option.Type != "number" || option.Min == nil || option.Max == nil {
		return 0, 0, fmt.Errorf("%w: combined compression budgets require numeric target-size metadata", ErrUnsupported)
	}
	step := 1.0
	if option.Step != nil {
		step = *option.Step
	}
	if math.Abs(*option.Min-math.Round(*option.Min)) > 1e-9 || math.Abs(*option.Max-math.Round(*option.Max)) > 1e-9 || math.Abs(step-math.Round(step)) > 1e-9 || math.Round(step) != 1 {
		return 0, 0, fmt.Errorf("%w: combined compression budgets require whole-megabyte target-size steps", ErrUnsupported)
	}
	minMB := int64(math.Round(*option.Min))
	maxMB := int64(math.Round(*option.Max))
	workerMaxMB := MaxOutputBytes / (1 << 20)
	if maxMB > workerMaxMB {
		maxMB = workerMaxMB
	}
	if minMB < 1 || maxMB < minMB {
		return 0, 0, fmt.Errorf("%w: target-size metadata is outside the Worker output ceiling", ErrUnsupported)
	}
	return minMB, maxMB, nil
}

func copyOptions(options map[string]string) map[string]string {
	copy := make(map[string]string, len(options)+1)
	for key, value := range options {
		copy[key] = value
	}
	return copy
}
