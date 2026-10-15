package com.schroeter.conv.profiles;

import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Numeric release comparison for catalog compatibility bounds. Qualifiers such as -SNAPSHOT do not change the release line. */
record AppVersion(int major, int minor, int patch) implements Comparable<AppVersion> {
    private static final Pattern VERSION = Pattern.compile("^[vV]?(\\d+)\\.(\\d+)(?:\\.(\\d+))?(?:[-+][0-9A-Za-z.-]+)?$");

    static AppVersion parse(String raw) {
        Matcher match = VERSION.matcher(raw == null ? "" : raw.trim());
        if (!match.matches()) throw new IllegalArgumentException("Version bounds must use numeric major.minor.patch versions.");
        try {
            return new AppVersion(Integer.parseInt(match.group(1)), Integer.parseInt(match.group(2)),
                    match.group(3) == null ? 0 : Integer.parseInt(match.group(3)));
        } catch (NumberFormatException e) {
            throw new IllegalArgumentException("Version bound is outside the supported range.", e);
        }
    }

    static boolean supports(String applicationVersion, String minimumInclusive, String maximumExclusive) {
        AppVersion current = parse(applicationVersion);
        if (minimumInclusive != null && !minimumInclusive.isBlank() && current.compareTo(parse(minimumInclusive)) < 0) return false;
        return maximumExclusive == null || maximumExclusive.isBlank() || current.compareTo(parse(maximumExclusive)) < 0;
    }

    @Override
    public int compareTo(AppVersion other) {
        int majorResult = Integer.compare(major, other.major);
        if (majorResult != 0) return majorResult;
        int minorResult = Integer.compare(minor, other.minor);
        return minorResult != 0 ? minorResult : Integer.compare(patch, other.patch);
    }
}
