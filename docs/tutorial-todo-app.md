# Tutorial: Build a Todo App with Railyard

This tutorial walks you through building a Go-based Todo List API using Railyard to orchestrate multiple Claude Code agents. By the end, you'll have working code written by AI agents — coordinated, tested, and merged automatically.

**What you'll learn:**
- Setting up a project for Railyard
- Breaking work into cars with dependencies
- Starting engines and watching them build your app
- Monitoring progress and handling completions

**Time:** ~15 minutes of setup, then sit back and watch.

---

## Prerequisites

Before starting, make sure you have:

- **Railyard** installed (`ry` binary on your PATH) — see the [quickstart](../quickstart.sh) or [README](../README.md)
- **Dolt** running on port 3306 (or whichever port you configured)
- **Claude Code CLI** installed (`npm install -g @anthropic-ai/claude-code`)
- **tmux** installed
- **Go 1.25+** installed
- An initialized Railyard database (`ry db init`)

If you haven't done any of this yet, the fastest path is:

```bash
cd /path/to/railyard
./quickstart.sh
```

---

## Step 1: Create Your Project

Start by creating a new Go project for the Todo app.

```bash
mkdir -p ~/projects/todo-app
cd ~/projects/todo-app

go mod init github.com/yourname/todo-app
git init
git commit --allow-empty -m "Initial commit"
```

Create a basic project structure:

```bash
mkdir -p cmd/server internal/todo
```

---

## Step 2: Configure Railyard

Create a `railyard.yaml` in your project root. This tells Railyard about your project structure, tracks, and conventions.

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
    conventions:
      go_version: "1.25"
      style: "stdlib-first, no frameworks"
      test_framework: "stdlib table-driven tests"
      patterns: "net/http for routing, encoding/json for serialization"
```

Key choices here:
- **`engine_slots: 2`** — up to 2 agents work in parallel on backend tasks
- **`conventions`** — tells agents your preferred style. They'll follow these when writing code
- **`file_patterns`** — scopes what files belong to this track

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

## Step 4: Plan Your Work — Create Cars

Now the fun part. Think about what a Todo API needs, then create cars (work items) for each piece. The key is structuring them with **dependencies** so Railyard builds things in the right order.

Here's our plan:

```
1. Todo model & in-memory store     (no dependencies — start here)
2. GET /todos endpoint              (depends on #1)
3. POST /todos endpoint             (depends on #1)
4. DELETE /todos/:id endpoint       (depends on #1)
5. Main server entrypoint           (depends on #2, #3, #4)
```

### Create the foundation car

```bash
ry car create -c railyard.yaml \
  --title "Add Todo model and in-memory store" \
  --track backend \
  --type task \
  --priority 0 \
  --description "Create internal/todo/model.go with a Todo struct (ID, Title, Done, CreatedAt). Create internal/todo/store.go with an in-memory Store that supports Add, List, Get, Delete, and Toggle operations. Use a sync.Mutex for thread safety. Include unit tests in store_test.go with table-driven tests for all operations."
```

The output will show the car ID (e.g., `car-a1b2c`). Save it — you'll need it for dependencies.

```bash
# Save the ID (your actual ID will differ)
MODEL_CAR=car-a1b2c
```

### Create the endpoint cars

```bash
GET_CAR=$(ry car create -c railyard.yaml \
  --title "Add GET /todos endpoint" \
  --track backend \
  --type task \
  --priority 1 \
  --description "Create internal/todo/handlers.go with a HandleListTodos function. It should return all todos as JSON (200 OK). Wire it to GET /todos. Include a test in handlers_test.go using httptest." \
  2>&1 | grep -oP 'car-\w+')

POST_CAR=$(ry car create -c railyard.yaml \
  --title "Add POST /todos endpoint" \
  --track backend \
  --type task \
  --priority 1 \
  --description "Add HandleCreateTodo to internal/todo/handlers.go. Accept JSON body with a 'title' field, create the todo in the store, return 201 with the created todo as JSON. Return 400 for missing/empty title. Include tests." \
  2>&1 | grep -oP 'car-\w+')

DELETE_CAR=$(ry car create -c railyard.yaml \
  --title "Add DELETE /todos/:id endpoint" \
  --track backend \
  --type task \
  --priority 1 \
  --description "Add HandleDeleteTodo to internal/todo/handlers.go. Parse the todo ID from the URL path, delete from the store, return 204 on success and 404 if not found. Include tests." \
  2>&1 | grep -oP 'car-\w+')

SERVER_CAR=$(ry car create -c railyard.yaml \
  --title "Add main server entrypoint" \
  --track backend \
  --type task \
  --priority 2 \
  --description "Create cmd/server/main.go. Set up an HTTP server on :8080 with routes: GET /todos, POST /todos, DELETE /todos/{id}. Use the todo.Store and handler functions from internal/todo/. Print a startup message with the port. Include a health check at GET /health returning 200 OK." \
  2>&1 | grep -oP 'car-\w+')
```

### Set up dependencies

The endpoint cars depend on the model, and the server depends on the endpoints:

```bash
# Endpoints depend on the model
ry car dep add -c railyard.yaml "$GET_CAR" --blocked-by "$MODEL_CAR"
ry car dep add -c railyard.yaml "$POST_CAR" --blocked-by "$MODEL_CAR"
ry car dep add -c railyard.yaml "$DELETE_CAR" --blocked-by "$MODEL_CAR"

# Server depends on all endpoints
ry car dep add -c railyard.yaml "$SERVER_CAR" --blocked-by "$GET_CAR"
ry car dep add -c railyard.yaml "$SERVER_CAR" --blocked-by "$POST_CAR"
ry car dep add -c railyard.yaml "$SERVER_CAR" --blocked-by "$DELETE_CAR"
```

### Verify the plan

```bash
ry car list -c railyard.yaml
```

You should see all 5 cars. Check what's ready to work:

```bash
ry car ready -c railyard.yaml --track backend
```

Only the model car should be ready — everything else is blocked.

---

## Step 5: Start Railyard

Launch the full orchestration:

```bash
ry start -c railyard.yaml --engines 2
```

This creates a tmux session with:
- **Dispatch** pane — the planning agent (you can talk to it to add more work)
- **Yardmaster** pane — the supervisor (monitors engines, merges branches, runs tests)
- **Engine 0 & 1** — worker agents that claim and execute cars

### Watch the action

```bash
tmux attach -t railyard
```

Use `Ctrl-b` then arrow keys to switch between panes. Use `Ctrl-b d` to detach.

---

## Step 6: Watch the Build Unfold

Here's what happens automatically:

### Phase 1: Foundation
1. **Engine 0** claims "Add Todo model and in-memory store" (the only ready car)
2. Engine 1 has nothing to do yet — it polls and waits
3. Engine 0 spawns a Claude Code session on branch `ry/yourname/backend/car-a1b2c`
4. The agent writes `model.go`, `store.go`, and `store_test.go`
5. Agent calls `ry complete` when done

### Phase 2: Merge & Unblock
6. **Yardmaster** notices the completed car
7. Runs `go test ./...` on the branch
8. Tests pass — merges branch to main via `ry switch`
9. Three endpoint cars are now **unblocked** and become ready

### Phase 3: Parallel Work
10. **Engine 0** claims GET endpoint, **Engine 1** claims POST endpoint
11. Both work in parallel on separate branches — no conflicts
12. As each completes, Yardmaster merges them
13. DELETE endpoint gets picked up next

### Phase 4: Final Assembly
14. Once all three endpoints are merged, the server car unblocks
15. An engine claims it, writes `cmd/server/main.go`
16. Yardmaster merges the final piece

### Monitor progress

While this is running, you can check on things from another terminal:

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

---

## Step 7: Verify the Result

Once all cars are done, check the final state:

```bash
ry car list -c railyard.yaml
```

All 5 cars should show status `done`. Your project now has:

```
todo-app/
  cmd/server/main.go          # HTTP server on :8080
  internal/todo/
    model.go                   # Todo struct
    store.go                   # In-memory store with mutex
    store_test.go              # Table-driven tests for store
    handlers.go                # HTTP handlers (list, create, delete)
    handlers_test.go           # Handler tests with httptest
  go.mod
  railyard.yaml
```

Run the tests yourself:

```bash
go test ./...
```

Start the server:

```bash
go run ./cmd/server/
```

Test it:

```bash
# Create a todo
curl -X POST http://localhost:8080/todos \
  -H 'Content-Type: application/json' \
  -d '{"title": "Learn Railyard"}'

# List all todos
curl http://localhost:8080/todos

# Delete a todo (use the ID from the create response)
curl -X DELETE http://localhost:8080/todos/<id>
```

---

## Step 8: Stop Railyard

When you're done:

```bash
ry stop -c railyard.yaml
```

This gracefully shuts down all engines, Dispatch, and Yardmaster.

---

## Tips and Next Steps

### Writing Good Car Descriptions

The quality of agent output depends heavily on your car descriptions. Good descriptions:
- **State the goal clearly** — what should exist when the car is done?
- **Specify the file paths** — agents work faster when they know where to put things
- **Include conventions** — "use table-driven tests", "return JSON", "handle errors with HTTP status codes"
- **Define edge cases** — "return 400 for empty title", "return 404 if not found"

### Dependency Strategy

- **Layer dependencies by data flow** — models first, then logic, then integration
- **Parallelize where possible** — independent endpoints can run simultaneously
- **Keep cars focused** — one concern per car. Smaller cars complete faster and merge cleaner

### Scaling Up

For larger projects, consider multiple tracks:

```yaml
tracks:
  - name: backend
    language: go
    file_patterns: ["cmd/**", "internal/**", "*.go"]
    engine_slots: 2

  - name: frontend
    language: typescript
    file_patterns: ["web/**", "*.ts", "*.tsx"]
    engine_slots: 2

  - name: infra
    language: mixed
    file_patterns: ["Dockerfile", "docker-compose.yaml", ".github/**"]
    engine_slots: 1
```

### Using Dispatch for Planning

Instead of manually creating cars, you can talk to the Dispatch agent:

```bash
tmux attach -t railyard
# Switch to the Dispatch pane (Ctrl-b, arrow keys)
```

Tell Dispatch what you want to build, and it will decompose your request into structured cars with dependencies automatically.

### Iterating on Completed Work

After the initial build, you can create new cars for enhancements:

```bash
ry car create -c railyard.yaml \
  --title "Add PUT /todos/:id endpoint for toggling done status" \
  --track backend \
  --type task \
  --priority 1 \
  --description "Add HandleToggleTodo to handlers.go. PUT /todos/{id} toggles the Done field. Return the updated todo as JSON (200) or 404 if not found. Include tests."
```

Engines will pick it up automatically if Railyard is still running, or on the next `ry start`.
