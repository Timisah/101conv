# Repository agent instructions

Read this file before making substantial changes. Project documentation, not prior chat context, is the durable source of truth:

1. [Master plan](docs/MASTER_PLAN.md): product direction, UX principles, milestone dependencies, non-goals and release gates.
2. [Project status](docs/PROJECT_STATUS.md): evidence-based implementation/verification state and current priorities.
3. [Feature backlog](docs/FEATURE_REQUESTS.md): requested capabilities and their proposed acceptance criteria.
4. [Decisions](docs/DECISIONS.md): accepted architecture constraints and open decisions.
5. Existing implementation-specific documentation in `docs/`.

## Non-negotiable engineering principles

- **Clean, modular code is a feature.** Prefer small, cohesive packages/components with explicit contracts, focused tests and clear ownership. Do not append more unrelated functionality to `web/src/App.tsx`, or build a monolithic all-purpose backend handler. Refactor incrementally with regression coverage; do not start unrelated rewrites.
- **Mobile-first, progressively disclosed UX.** A basic convert/compress task must remain simple on a phone; expose specialist options only when relevant. Test a narrow mobile viewport, touch interactions, error states, and desktop accessibility.
- **Preserve users and installations.** Do not discard databases, mounted data, accounts, retention state, presets, profile/engine installations, jobs or immutable automation revisions. Keep Portainer/Compose deployment and existing volume paths working. Migrate additively with upgrade and rollback plans.
- **Worker isolation and supply-chain trust are invariants.** No arbitrary user-supplied commands/FFmpeg flags, network privileges, Docker socket, unrestricted host mounts, or unsigned-source trust bypasses. Review provenance, licenses and pinned dependencies. Treat uploaded files as hostile.
- **Execution must remain deterministic.** Resolve exact engine/provider versions, options and capabilities into the immutable job specification. Preserve historical versions and queued-job compatibility. Never silently downgrade quality, HDR properties or fidelity.
- **Prefer one coherent capability model.** Engines execute; capability providers declare operations and supported settings; user presets save intent and validated values. Current legacy `profiles` are a migration boundary, not a reason for competing selectors in everyday UI.
- **Truth over green checkmarks.** Distinguish implemented, tested, manually verified, unverified and experimental. Validate actual output content and metadata, not merely accepted requests. Never suppress failing tests or delete production Docker resources to pass CI.
- **Security and privacy by default.** Never log documents, tokens, uploaded content or sensitive metadata. Apply quotas, retention, ownership checks, bounded resource use and explicit approval for destructive/irreversible operations.
- **Maintain discoverability honestly.** Only document or advertise supported, tested capabilities. Never generate fake SEO content or imply Google/ChatGPT recommendations are guaranteed.

## Agent workflow

1. Read plan/status/decision documents, inspect the current `main` commit, existing code, tests and relevant issues.
2. Pick only the requested or approved scope. The plan is a set of goals, **not authorization to implement everything** or to change priorities unilaterally.
3. Define bounded interfaces, data flow, migrations, threats, and tests before significant changes. Keep changes reviewable and composable.
4. Implement with feature-focused modules and typed contracts. Use existing Go Core/PostgreSQL/Worker/React architecture.
5. Verify unit, integration, compatibility, security, browser/mobile and deployment paths appropriate to the change. Report exactly what ran and CI evidence.
6. Update `docs/PROJECT_STATUS.md` with verified progress; update `docs/MASTER_PLAN.md` only for approved roadmap changes; add decision entries for material architectural choices and keep technical docs synchronized.
7. Report changed files, migration impact, tests (including failures/skips), limitations, and next steps. Never call a commit or green badge proof of a successful real deployment.

### Fresh-session “Continue development” protocol

When a fresh session is asked to **Continue development**, first locate the actual repository, inspect its remote, branch, commit, worktree and current `origin/main`, and preserve any existing local work. Read this file and the linked plan, status, backlog and decision documents before selecting scope. Inspect the current GitHub issues and CI evidence as well as the code; do not assume that an old checkout, an open issue, or an implementation commit is current proof.

Select exactly one highest-priority approved, unblocked task that is supported by the roadmap and issue state. P0 correctness, security, compatibility, CI and release-gate repairs take precedence over new feature milestones. If CI or project tracking is known to be incorrect, repair and verify those gates before beginning another feature milestone. Stop for owner input only when the selected work requires an owner decision identified by the plan, issue, or decision log.

Create a focused branch from the latest synchronized `origin/main` (or report a genuine dirty-worktree conflict without overwriting it). Keep the change modular and backwards-compatible, including mobile and security requirements. Run the relevant unit, integration, compatibility, security, browser/mobile, Docker and backup/restore checks; distinguish passed, failed, skipped and environment-blocked checks. Update `docs/PROJECT_STATUS.md` and related tracking only from verified evidence, and reconcile GitHub issue state without closing work whose acceptance or release gate remains open.

Commit the focused change with a clear message and push it. Use a feature branch and pull request when review or branch protection requires it, but treat both as intermediate delivery steps. After every required check has actually passed, integrate the verified commit into `main` automatically, push `main`, and verify the resulting `main` commit and CI run. Attach any created pull request to the task and reconcile or close it only after its changes are integrated. Never merge failing or unverified changes, override branch protections, force-push, rewrite history, weaken tests, delete production resources, or start another milestone to hide a failure. If external permissions or an irreducible blocker prevents integration, leave `main` intact and report the exact blocker.

The owner explicitly authorizes automatic merging of completed, verified, roadmap-approved development work into `main` without asking for confirmation each time. Owner approval is still required for destructive changes, breaking migrations, security-policy exceptions, production secrets, branding decisions, and major scope changes outside the approved roadmap.

## Authority and documentation policy

- The project owner approves branding, major scope changes, breaking API changes, release readiness and architecture departures.
- Status entries must have supporting code paths, tests or release evidence. An implementation commit alone does not make an external-deployment guarantee.
- Do not mark aspirational features as shipped, remove roadmap commitments, or rewrite history to match a newer design.
- Current project name **101conv** remains an implementation identifier until a distinct brand is chosen and a compatible migration plan approved.
