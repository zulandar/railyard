# Tutorial: Build a Todo App with Railyard

This tutorial walks you through building a Todo List API using Railyard to orchestrate multiple Claude Code agents. The example uses a Dispatch session to plan the work — just describe what you want, and Railyard decomposes it into tasks, manages dependencies, and coordinates parallel agents to build it.

**Railyard is language-agnostic.** This tutorial shows config examples for Go, PHP, and Node.js. Pick your stack — the workflow is the same.

**What you'll learn:**
- Configuring Railyard for any language
- Using Dispatch to plan work through conversation
- How engines claim cars, work in parallel, and merge results
- Monitoring progress and verifying the build

**Time:** ~10 minutes of setup, then sit back and watch.

---

## Prerequisites

Before starting, make sure you have:

- **Railyard** installed (`ry` binary on your PATH) — see the [quickstart](../quickstart.sh) or [README](../README.md)
- **Dolt** running on port 3306 (or whichever port you configured)
- **Claude Code CLI** installed (`npm install -g @anthropic-ai/claude-code`)
- **tmux** installed
- Your language toolchain installed (Go, PHP, Node.js, etc.)
- An initialized Railyard database (`ry db init`)

If you haven't done any of this yet, the fastest path is:

```bash
cd /path/to/railyard
./quickstart.sh
```

---

## Step 1: Create Your Project

Start with an empty project and a git repo. Here are examples for different languages:

**Go:**
```bash
mkdir -p ~/projects/todo-app && cd ~/projects/todo-app
go mod init github.com/yourname/todo-app
git init && git commit --allow-empty -m "Initial commit"
```

**PHP (Laravel):**
```bash
composer create-project laravel/laravel ~/projects/todo-app
cd ~/projects/todo-app
git init && git add -A && git commit -m "Initial commit"
```

**Node.js (Express):**
```bash
mkdir -p ~/projects/todo-app && cd ~/projects/todo-app
npm init -y && npm install express
git init && git commit --allow-empty -m "Initial commit"
```

---

## Step 2: Configure Railyard

Create a `railyard.yaml` in your project root. This tells Railyard about your project structure, language conventions, and how to run tests.

Pick the config for your language:

### Go
```yaml
owner: yourname
repo: ~/projects/todo-app

dolt:
  host: 127.0.0.1
  port: 3306

tracks:
  - name: backend
    language: go
    file_patterns: ["cmd/**", "internal/**", "*.go"]
    engine_slots: 2
    test_command: "go test ./..."
    conventions:
      go_version: "1.25"
      style: "stdlib-first, no frameworks"
      test_framework: "stdlib table-driven tests"
      patterns: "net/http for routing, encoding/json for serialization"
```

### PHP (Laravel)
```yaml
owner: yourname
repo: ~/projects/todo-app

dolt:
  host: 127.0.0.1
  port: 3306

tracks:
  - name: backend
    language: php
    file_patterns: ["app/**", "routes/**", "database/**", "tests/**"]
    engine_slots: 2
    test_command: "php artisan test"
    conventions:
      framework: "Laravel 11"
      style: "PSR-12, Eloquent ORM"
      test_framework: "PHPUnit with Laravel test helpers"
      patterns: "Resource controllers, Form Requests for validation, API Resources for responses"
```

### Node.js (Express)
```yaml
owner: yourname
repo: ~/projects/todo-app

dolt:
  host: 127.0.0.1
  port: 3306

tracks:
  - name: backend
    language: javascript
    file_patterns: ["src/**", "routes/**", "*.js"]
    engine_slots: 2
    test_command: "npm test"
    conventions:
      runtime: "Node.js 22"
      framework: "Express 5"
      style: "ES modules, async/await"
      test_framework: "Jest with supertest"
      patterns: "Router middleware, express-validator, JSON responses"
```

### Key fields explained

| Field | Purpose |
|---|---|
| `engine_slots` | Max parallel agents on this track |
| `test_command` | Shell command Yardmaster runs before merging (defaults to `go test ./...` if unset) |
| `conventions` | Free-form metadata passed to agents as context — they follow these when writing code |
| `file_patterns` | Scopes what files belong to this track |

---

## Step 3: Initialize the Database

```bash
ry db init -c railyard.yaml
```

This creates the Dolt database tables for your project. You only need to do this once.

Verify it worked:

```bash
ry status -c railyard.yaml
```

You should see an empty dashboard — no cars, no engines, ready to go.

---

## Step 4: Start Railyard and Plan with Dispatch

Instead of manually creating cars, we'll use **Dispatch** — Railyard's planning agent. Start the full orchestration:

```bash
ry start -c railyard.yaml --engines 2
```

This launches a tmux session with four panes:
- **Dispatch** — the planning agent (you talk to this one)
- **Yardmaster** — the supervisor (monitors, tests, merges)
- **Engine 0 & Engine 1** — worker agents (claim and execute cars)

### Attach and talk to Dispatch

```bash
tmux attach -t railyard
```

Navigate to the **Dispatch pane** (use `Ctrl-b` then arrow keys). You'll see an interactive Claude Code session. Tell it what you want to build:

```
Build a Todo List API with these endpoints:
- GET /todos (list all)
- POST /todos (create, requires title)
- DELETE /todos/:id (delete by ID)
- GET /health (health check)

Include an in-memory store, tests for everything, and a main entrypoint
that starts a server on port 8080.
```

### What Dispatch does

Dispatch reads your `railyard.yaml` (language, conventions, patterns) and automatically:

1. **Creates an epic** for the feature
2. **Decomposes** it into atomic tasks — typically:
   - Data model / store (foundation)
   - Individual endpoint handlers (can parallelize)
   - Main entrypoint / wiring (depends on handlers)
3. **Sets dependencies** — endpoints blocked by model, server blocked by endpoints
4. **Sets priorities** — foundation at P0, endpoints at P1, integration at P2

You'll see Dispatch run `ry car create` and `ry car dep add` commands in real time. It uses your language and conventions to tailor descriptions — a PHP decomposition will reference Eloquent models and Laravel routes, while a Go one will reference structs and net/http.

### Verify the plan

After Dispatch finishes, check the cars from another terminal:

```bash
# See all created cars
ry car list -c railyard.yaml

# See what's ready for engines to claim
ry car ready -c railyard.yaml --track backend
```

Only the foundation car (model/store) should be ready — everything else is blocked by dependencies.

---

## Step 5: Watch the Build Unfold

Once Dispatch creates the cars, engines pick them up immediately. Here's the automated flow:

### Phase 1: Foundation
1. **Engine 0** claims the model/store car (the only one with no blockers)
2. Engine 1 has nothing to do yet — it polls and waits
3. Engine 0 spawns a Claude Code session on an isolated branch
4. The agent writes the data model, store logic, and tests
5. Agent calls `ry complete` when done

### Phase 2: Merge and Unblock
6. **Yardmaster** notices the completed car
7. Runs your `test_command` on the branch
8. Tests pass — merges branch to main via `ry switch`
9. Endpoint cars are now **unblocked** and become ready

### Phase 3: Parallel Work
10. **Engine 0** claims one endpoint, **Engine 1** claims another
11. Both work in parallel on separate branches — no conflicts
12. As each completes, Yardmaster tests and merges
13. Remaining endpoints get picked up

### Phase 4: Final Assembly
14. Once all endpoints are merged, the server entrypoint car unblocks
15. An engine claims it, writes the main entry point and routing
16. Yardmaster merges the final piece

### Monitor progress

While engines work, check on things from another terminal:

```bash
# Dashboard view
ry status -c railyard.yaml

# Auto-refreshing dashboard
ry status -c railyard.yaml --watch

# See what engines are doing
ry engine list -c railyard.yaml

# Check a specific car
ry car show <car-id> -c railyard.yaml
```

Or watch the tmux panes directly — you'll see agents writing code in real time.

---

## Step 6: Verify the Result

Once all cars show status `done`:

```bash
ry car list -c railyard.yaml
```

Run the tests yourself:

**Go:**
```bash
go test ./...
go run ./cmd/server/
```

**PHP:**
```bash
php artisan test
php artisan serve
```

**Node.js:**
```bash
npm test
node src/index.js
```

Test the API:

```bash
# Create a todo
curl -X POST http://localhost:8080/todos \
  -H 'Content-Type: application/json' \
  -d '{"title": "Learn Railyard"}'

# List all todos
curl http://localhost:8080/todos

# Delete a todo
curl -X DELETE http://localhost:8080/todos/<id>

# Health check
curl http://localhost:8080/health
```

---

## Step 7: Stop Railyard

When you're done:

```bash
ry stop -c railyard.yaml
```

This gracefully shuts down all engines, Dispatch, and Yardmaster.

---

## Tips and Next Steps

### Dispatch vs. Manual Car Creation

**Use Dispatch** (recommended for most work):
- Describe what you want in natural language
- Dispatch handles decomposition, dependencies, and priorities
- Best for features, epics, and multi-step work

**Use manual `ry car create`** when you want precise control:
- One-off bug fixes with specific instructions
- Adding a single well-defined task
- Scripted/automated workflows

### Iterating on Completed Work

After the initial build, talk to Dispatch again:

```
Add a PUT /todos/:id endpoint that toggles the done status.
Also add a PATCH /todos/:id endpoint for updating the title.
```

Or create a car directly:

```bash
ry car create -c railyard.yaml \
  --title "Add PUT /todos/:id for toggling done" \
  --track backend \
  --type task \
  --priority 1 \
  --description "Toggle the Done field. Return updated todo (200) or 404."
```

Engines pick up new cars automatically while Railyard is running.

### Multi-Track Projects

For full-stack apps, define multiple tracks:

```yaml
tracks:
  - name: backend
    language: php
    file_patterns: ["app/**", "routes/**", "database/**", "tests/**"]
    engine_slots: 2
    test_command: "php artisan test"
    conventions:
      framework: "Laravel 11"

  - name: frontend
    language: typescript
    file_patterns: ["resources/js/**", "*.ts", "*.tsx", "*.vue"]
    engine_slots: 2
    test_command: "npm test"
    conventions:
      framework: "Vue 3"
      styling: "Tailwind CSS"
```

Dispatch automatically creates cross-track dependencies when needed (e.g., frontend login page blocked by backend auth endpoint).

### Writing Good Car Descriptions

Whether Dispatch generates them or you write them manually, good descriptions:
- **State the goal clearly** — what should exist when the car is done?
- **Specify file paths** — agents work faster when they know where to put things
- **Include conventions** — "use Eloquent", "use table-driven tests", "return JSON"
- **Define edge cases** — "return 400 for empty title", "return 404 if not found"
