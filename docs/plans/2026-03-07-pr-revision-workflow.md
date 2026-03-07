# PR Revision Workflow Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable the full PR revision loop: detect review feedback on pr_open cars, route them back to engines for rework on the existing branch.

**Architecture:** Add a `handlePrOpenCars` phase to the yardmaster daemon that polls GitHub PR status via `gh pr view`. Add `pr_open` to `ValidTransitions`. Add `CheckoutExistingBranch` to the engine git layer so revision cars resume on their existing branch instead of branching fresh off main.

**Tech Stack:** Go, gh CLI (JSON output), GORM/SQLite (tests), git

---

## Task 1: Add pr_open to ValidTransitions

**Files:**
- Modify: `internal/car/car.go:48-57`
- Modify: `internal/car/car_test.go:124-131`

**Step 1: Write the failing test**

In `internal/car/car_test.go`, update `TestValidTransitions_AllStatusesPresent` to include `"pr_open"` and add a new test for the specific transitions:

```go
// In TestValidTransitions_AllStatusesPresent, add "pr_open" to expected:
expected := []string{"draft", "open", "ready", "claimed", "in_progress", "blocked", "merge-failed", "done", "pr_open"}

// New test:
func TestValidTransitions_PrOpen(t *testing.T) {
    targets, ok := ValidTransitions["pr_open"]
    if !ok {
        t.Fatal("ValidTransitions missing pr_open")
    }
    want := map[string]bool{"open": true, "merged": true, "cancelled": true}
    if len(targets) != len(want) {
        t.Fatalf("pr_open targets = %v, want %v", targets, want)
    }
    for _, s := range targets {
        if !want[s] {
            t.Errorf("unexpected pr_open target: %q", s)
        }
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/car/ -run "TestValidTransitions" -v`
Expected: FAIL — `ValidTransitions missing key "pr_open"`

**Step 3: Write minimal implementation**

In `internal/car/car.go:48-57`, add the `pr_open` entry to `ValidTransitions`:

```go
var ValidTransitions = map[string][]string{
    "draft":        {"open"},
    "open":         {"ready", "cancelled", "blocked"},
    "ready":        {"claimed", "blocked"},
    "claimed":      {"in_progress", "blocked"},
    "in_progress":  {"done", "blocked"},
    "done":         {"merged", "merge-failed"},
    "blocked":      {"open", "ready"},
    "merge-failed": {"done"},
    "pr_open":      {"open", "merged", "cancelled"},
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/car/ -run "TestValidTransitions" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/car/car.go internal/car/car_test.go
git commit -m "feat: add pr_open to ValidTransitions status machine"
```

---

## Task 2: Add handlePrOpenCars daemon function

**Files:**
- Modify: `internal/yardmaster/daemon.go` (add function + call in loop)
- Create test: `internal/yardmaster/daemon_test.go` (add tests)

**Step 1: Write the failing test**

Add to `internal/yardmaster/daemon_test.go`:

```go
func TestHandlePrOpenCars_ChangesRequested(t *testing.T) {
    db := testDB(t)

    // Create a pr_open car.
    db.Create(&models.Car{
        ID:     "car-pro1",
        Branch: "ry/backend/car-pro1",
        Status: "pr_open",
        Track:  "backend",
    })

    // Mock gh by providing a ghPRViewer that returns changes_requested.
    viewer := &mockPRViewer{
        reviewDecision: "CHANGES_REQUESTED",
        state:          "OPEN",
        reviews:        []prReview{{Body: "Fix the error handling", Author: "reviewer1"}},
    }

    var buf bytes.Buffer
    err := handlePrOpenCars(db, viewer, &buf)
    if err != nil {
        t.Fatalf("handlePrOpenCars: %v", err)
    }

    var c models.Car
    db.First(&c, "id = ?", "car-pro1")
    if c.Status != "open" {
        t.Errorf("status = %q, want %q", c.Status, "open")
    }
    if c.Assignee != "" {
        t.Errorf("assignee = %q, want empty", c.Assignee)
    }

    // Check progress note was written.
    var notes []models.CarProgress
    db.Where("car_id = ?", "car-pro1").Find(&notes)
    if len(notes) == 0 {
        t.Error("expected progress note with review comments")
    }
}

func TestHandlePrOpenCars_Merged(t *testing.T) {
    db := testDB(t)

    db.Create(&models.Car{
        ID:     "car-pro2",
        Branch: "ry/backend/car-pro2",
        Status: "pr_open",
        Track:  "backend",
    })

    viewer := &mockPRViewer{state: "MERGED"}

    var buf bytes.Buffer
    err := handlePrOpenCars(db, viewer, &buf)
    if err != nil {
        t.Fatalf("handlePrOpenCars: %v", err)
    }

    var c models.Car
    db.First(&c, "id = ?", "car-pro2")
    if c.Status != "merged" {
        t.Errorf("status = %q, want %q", c.Status, "merged")
    }
}

func TestHandlePrOpenCars_Closed(t *testing.T) {
    db := testDB(t)

    db.Create(&models.Car{
        ID:     "car-pro3",
        Branch: "ry/backend/car-pro3",
        Status: "pr_open",
        Track:  "backend",
    })

    viewer := &mockPRViewer{state: "CLOSED"}

    var buf bytes.Buffer
    err := handlePrOpenCars(db, viewer, &buf)
    if err != nil {
        t.Fatalf("handlePrOpenCars: %v", err)
    }

    var c models.Car
    db.First(&c, "id = ?", "car-pro3")
    if c.Status != "cancelled" {
        t.Errorf("status = %q, want %q", c.Status, "cancelled")
    }
}

func TestHandlePrOpenCars_NoPrOpenCars(t *testing.T) {
    db := testDB(t)

    viewer := &mockPRViewer{state: "OPEN"}

    var buf bytes.Buffer
    err := handlePrOpenCars(db, viewer, &buf)
    if err != nil {
        t.Fatalf("handlePrOpenCars: %v", err)
    }
    // No cars to process — should succeed silently.
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/yardmaster/ -run "TestHandlePrOpenCars" -v`
Expected: FAIL — `handlePrOpenCars` undefined, `mockPRViewer` undefined

**Step 3: Write minimal implementation**

Add to `internal/yardmaster/daemon.go`:

```go
// prReview holds a single review comment from a PR.
type prReview struct {
    Body   string
    Author string
}

// prStatus holds the GitHub PR status for a branch.
type prStatus struct {
    State          string     // OPEN, MERGED, CLOSED
    ReviewDecision string     // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
    Reviews        []prReview
}

// PRViewer abstracts GitHub PR status lookups for testability.
type PRViewer interface {
    ViewPR(branch string) (*prStatus, error)
}

// ghPRViewer implements PRViewer using the gh CLI.
type ghPRViewer struct {
    repoSlug string // "owner/repo"
}

func (g *ghPRViewer) ViewPR(branch string) (*prStatus, error) {
    cmd := exec.Command("gh", "pr", "view", branch,
        "--repo", g.repoSlug,
        "--json", "state,reviewDecision,reviews")
    out, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("gh pr view %s: %w", branch, err)
    }

    var result struct {
        State          string `json:"state"`
        ReviewDecision string `json:"reviewDecision"`
        Reviews        []struct {
            Body   string `json:"body"`
            Author struct {
                Login string `json:"login"`
            } `json:"author"`
        } `json:"reviews"`
    }
    if err := json.Unmarshal(out, &result); err != nil {
        return nil, fmt.Errorf("parse gh pr view: %w", err)
    }

    ps := &prStatus{
        State:          result.State,
        ReviewDecision: result.ReviewDecision,
    }
    for _, r := range result.Reviews {
        ps.Reviews = append(ps.Reviews, prReview{Body: r.Body, Author: r.Author.Login})
    }
    return ps, nil
}

// handlePrOpenCars polls pr_open cars for GitHub review status and transitions
// them based on the PR state: changes_requested → open, merged → merged, closed → cancelled.
func handlePrOpenCars(db *gorm.DB, viewer PRViewer, out io.Writer) error {
    prCars, err := car.List(db, car.ListFilters{Status: "pr_open"})
    if err != nil {
        return err
    }

    for _, c := range prCars {
        if c.Branch == "" {
            continue
        }

        status, err := viewer.ViewPR(c.Branch)
        if err != nil {
            log.Printf("pr status for %s: %v", c.ID, err)
            continue
        }

        switch {
        case status.State == "MERGED":
            now := time.Now()
            db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
                "status":       "merged",
                "completed_at": now,
            })
            fmt.Fprintf(out, "PR merged for car %s — status → merged\n", c.ID)

        case status.State == "CLOSED":
            db.Model(&models.Car{}).Where("id = ?", c.ID).Update("status", "cancelled")
            fmt.Fprintf(out, "PR closed for car %s — status → cancelled\n", c.ID)

        case status.ReviewDecision == "CHANGES_REQUESTED":
            db.Model(&models.Car{}).Where("id = ?", c.ID).Updates(map[string]interface{}{
                "status":   "open",
                "assignee": "",
            })
            // Write review comments as progress notes.
            var reviewText strings.Builder
            reviewText.WriteString("PR review: changes requested\n")
            for _, r := range status.Reviews {
                if r.Body != "" {
                    fmt.Fprintf(&reviewText, "- @%s: %s\n", r.Author, r.Body)
                }
            }
            writeProgressNote(db, c.ID, "yardmaster", reviewText.String())
            fmt.Fprintf(out, "PR changes requested for car %s — status → open\n", c.ID)
        }
    }

    return nil
}
```

Add the `mockPRViewer` to the test file:

```go
type mockPRViewer struct {
    reviewDecision string
    state          string
    reviews        []prReview
    err            error
}

func (m *mockPRViewer) ViewPR(branch string) (*prStatus, error) {
    if m.err != nil {
        return nil, m.err
    }
    return &prStatus{
        State:          m.state,
        ReviewDecision: m.reviewDecision,
        Reviews:        m.reviews,
    }, nil
}
```

Add `"encoding/json"` to imports in `daemon.go`.

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/yardmaster/ -run "TestHandlePrOpenCars" -v`
Expected: PASS

**Step 5: Wire into daemon loop**

In `internal/yardmaster/daemon.go`, after Phase 5 (reconcileStaleCars, ~line 127), add:

```go
// Phase 5b: Poll pr_open cars for GitHub review feedback.
if cfg.RequirePR {
    prViewer := &ghPRViewer{repoSlug: cfg.Owner + "/" + cfg.Repo}
    if err := handlePrOpenCars(db, prViewer, out); err != nil {
        log.Printf("yardmaster pr review error: %v", err)
    }
}
```

**Step 6: Run full yardmaster tests**

Run: `go test ./internal/yardmaster/ -count=1`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/yardmaster/daemon.go internal/yardmaster/daemon_test.go
git commit -m "feat: add handlePrOpenCars daemon loop for PR review detection"
```

---

## Task 3: Add CheckoutExistingBranch to engine git layer

**Files:**
- Modify: `internal/engine/git.go`
- Modify: `internal/engine/git_test.go`

**Step 1: Write the failing test**

Add to `internal/engine/git_test.go`:

```go
func TestCheckoutExistingBranch(t *testing.T) {
    // Set up a repo with a remote that has an existing branch.
    bareDir := t.TempDir()
    parentDir := t.TempDir()

    run := func(dir string, args ...string) {
        t.Helper()
        cmd := exec.Command(args[0], args[1:]...)
        cmd.Dir = dir
        out, err := cmd.CombinedOutput()
        if err != nil {
            t.Fatalf("%v in %s: %s", args, dir, out)
        }
    }

    // Create bare remote.
    run(bareDir, "git", "init", "--bare", "-b", "main")

    // Clone, commit, push.
    run(parentDir, "git", "clone", bareDir, "repo")
    repoDir := filepath.Join(parentDir, "repo")
    run(repoDir, "git", "config", "user.email", "test@test.com")
    run(repoDir, "git", "config", "user.name", "test")
    run(repoDir, "git", "commit", "--allow-empty", "-m", "init")
    run(repoDir, "git", "push", "origin", "main")

    // Create a feature branch, add a file, push it.
    run(repoDir, "git", "checkout", "-b", "ry/backend/car-abc")
    os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("work"), 0644)
    run(repoDir, "git", "add", "feature.txt")
    run(repoDir, "git", "commit", "-m", "feature work")
    run(repoDir, "git", "push", "origin", "ry/backend/car-abc")

    // Go back to main.
    run(repoDir, "git", "checkout", "main")

    // Now checkout the existing branch.
    if err := CheckoutExistingBranch(repoDir, "ry/backend/car-abc"); err != nil {
        t.Fatalf("CheckoutExistingBranch: %v", err)
    }

    // Verify we're on the right branch.
    got := currentBranch(t, repoDir)
    if got != "ry/backend/car-abc" {
        t.Errorf("branch = %q, want %q", got, "ry/backend/car-abc")
    }

    // Verify the feature file exists.
    if _, err := os.Stat(filepath.Join(repoDir, "feature.txt")); err != nil {
        t.Error("feature.txt should exist on the checked-out branch")
    }
}

func TestCheckoutExistingBranch_EmptyDir(t *testing.T) {
    err := CheckoutExistingBranch("", "ry/test")
    if err == nil {
        t.Fatal("expected error for empty dir")
    }
}

func TestCheckoutExistingBranch_EmptyBranch(t *testing.T) {
    err := CheckoutExistingBranch("/tmp", "")
    if err == nil {
        t.Fatal("expected error for empty branch")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run "TestCheckoutExistingBranch" -v`
Expected: FAIL — `CheckoutExistingBranch` undefined

**Step 3: Write minimal implementation**

Add to `internal/engine/git.go`:

```go
// CheckoutExistingBranch fetches origin and checks out an existing remote branch.
// Used for revision cars that already have a pushed branch with prior work.
func CheckoutExistingBranch(wtDir, branch string) error {
    if wtDir == "" {
        return fmt.Errorf("engine: worktree directory is required")
    }
    if branch == "" {
        return fmt.Errorf("engine: branch name is required")
    }

    // Fetch to get latest remote refs.
    fetch := exec.Command("git", "fetch", "origin")
    fetch.Dir = wtDir
    if out, err := fetch.CombinedOutput(); err != nil {
        return fmt.Errorf("engine: fetch origin: %s", strings.TrimSpace(string(out)))
    }

    // Try checking out the local branch if it exists.
    checkout := exec.Command("git", "checkout", branch)
    checkout.Dir = wtDir
    if out, err := checkout.CombinedOutput(); err != nil {
        // Branch doesn't exist locally — create from remote tracking branch.
        checkoutRemote := exec.Command("git", "checkout", "-b", branch, "origin/"+branch)
        checkoutRemote.Dir = wtDir
        if rOut, rErr := checkoutRemote.CombinedOutput(); rErr != nil {
            return fmt.Errorf("engine: checkout existing branch %q: %s", branch, strings.TrimSpace(string(rOut)))
        }
        _ = out // suppress unused
    }

    // Pull latest changes on the branch.
    pull := exec.Command("git", "pull", "--ff-only", "origin", branch)
    pull.Dir = wtDir
    pull.CombinedOutput() // Non-fatal — branch may already be up to date.

    return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/engine/ -run "TestCheckoutExistingBranch" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/engine/git.go internal/engine/git_test.go
git commit -m "feat: add CheckoutExistingBranch for PR revision re-claims"
```

---

## Task 4: Engine loop uses CheckoutExistingBranch for revision cars

**Files:**
- Modify: `cmd/ry/engine.go:254-266`

**Step 1: Write the change**

In `cmd/ry/engine.go`, replace the ResetWorktree + CreateBranch block (~lines 254-266) with revision-aware logic:

```go
// Set up git branch — revision cars resume existing branch, new cars branch off base.
isRevision := claimed.CompletedAt != nil && claimed.Branch != ""
if isRevision {
    log.Printf("Revision car %s — checking out existing branch %s", claimed.ID, claimed.Branch)
    if err := engine.CheckoutExistingBranch(workDir, claimed.Branch); err != nil {
        log.Printf("checkout existing branch error: %v", err)
        sleepWithContext(ctx, pollInterval)
        continue
    }
} else {
    // Reset worktree to clean state at the car's base branch before branching.
    if err := engine.ResetWorktree(workDir, claimed.BaseBranch); err != nil {
        log.Printf("reset worktree error: %v", err)
        sleepWithContext(ctx, pollInterval)
        continue
    }
    // Create git branch from the car's base branch.
    if err := engine.CreateBranch(workDir, claimed.Branch, claimed.BaseBranch); err != nil {
        log.Printf("create branch error: %v", err)
        sleepWithContext(ctx, pollInterval)
        continue
    }
}
```

**Step 2: Run existing tests**

Run: `go test ./cmd/ry/ -count=1`
Expected: PASS (no regressions)

**Step 3: Commit**

```bash
git add cmd/ry/engine.go
git commit -m "feat: engine loop uses CheckoutExistingBranch for revision cars"
```

---

## Task 5: Full regression test

**Step 1: Run all affected packages**

```bash
go test ./internal/car/ ./internal/yardmaster/ ./internal/engine/ ./cmd/ry/ -count=1
```

Expected: All PASS

**Step 2: Final commit (if any fixups needed)**

```bash
git add -A && git commit -m "fix: address test regressions from PR revision workflow"
```

**Step 3: Push**

```bash
git push
```

**Step 4: Close bead**

```bash
bd close railyard-zyq.2 --reason="ValidTransitions updated, handlePrOpenCars daemon loop added, CheckoutExistingBranch implemented, engine loop revision-aware."
```
