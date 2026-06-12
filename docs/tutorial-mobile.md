# Tutorial: Build a Mobile App (React Native / Expo) with Railyard

Mobile is the stretch case for "any language." This tutorial uses **React Native via Expo** — it keeps prerequisites near-zero (no Xcode/Android Studio needed for the core loop) and stays in the JavaScript/TypeScript ecosystem most readers know. It also doubles as the **landing page for mobile**: Flutter, Swift, and Kotlin all have detection presets, pointed to at the end.

**What you'll learn:**
- What `ry init` actually detects on an Expo/React Native repo (shown honestly, including the gaps)
- Running a feature through dependency-ordered cars with **jest** as the merge gate
- How engines verify UI-adjacent work without a device, and why Playwright does **not** apply to native mobile

**Time:** ~10 minutes of setup, then the engines build.

> **Honesty note.** The `ry init` detection output below was captured from a **real run** on a minimal Expo-shaped project (the detection-relevant files: `package.json` with `expo`/`react-native`, `tsconfig.json`, `app.json`, an `app/index.tsx`). A full `npx create-expo-app` produces more files but the **detection result is the same**. Steps that spawn live AI engines are marked; no agent output is fabricated here.

---

## Prerequisites

- **`ry`** — `curl -fsSL https://raw.githubusercontent.com/zulandar/railyard/main/install.sh | sh`
- **Docker** running — Railyard starts MySQL in a container for you
- **An AI coding CLI** — e.g. Claude Code: `npm install -g @anthropic-ai/claude-code`
- **tmux**
- **Node.js** — Expo + jest run on Node

---

## Step 1: Create the Expo app

```bash
npx create-expo-app@latest mobile-app
cd mobile-app
git init -b main   # create-expo-app may already init git
git remote add origin https://github.com/yourname/mobile-app.git
git add -A && git commit -m "Initial Expo app"
```

A default Expo app is TypeScript-based: it has a `package.json` (with `expo`, `react-native`), a `tsconfig.json`, an `app.json`, and `.tsx` screens under `app/`.

---

## Step 2: `ry init` — what gets detected (and what doesn't)

```console
$ ry init --yes
Detected git repository: /home/you/projects/mobile-app
Detected remote: https://github.com/yourname/mobile-app.git
Detected owner: yourname
Detected languages: typescript

Wrote /home/you/projects/mobile-app/railyard.yaml
Seeded 1 track(s) and config for owner "yourname"

Railyard initialized successfully!
```

The generated track:

```yaml
tracks:
  - name: frontend
    language: typescript
    file_patterns: ["**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx"]
    engine_slots: 2
    test_command: "npm test"
```

**What this gets right:** the `tsconfig.json` makes it a TypeScript track, and the patterns include `**/*.tsx` — so your screens and components are routed correctly. With `"test": "jest"` in `package.json`, `npm test` runs your jest suite, which is the right merge gate.

**What it does NOT do (honest gaps):**
- It's detected as a **generic `typescript` frontend track**, not a React-Native- or "mobile"-aware track. There's no RN-specific convention injected, and the track is named `frontend`, not `mobile`. (Railyard's `mobile` classification today applies to Swift/Kotlin/Dart — see below — not to RN, which presents as a Node project.)
- A **managed** Expo app has no native `android/`/`ios/` directories, so detection stays clean. A **bare** RN/Expo app that has ejected to native code *would* additionally trip the Kotlin (AndroidManifest) and Swift (`ios/*.xcodeproj`) markers, producing extra `mobile` tracks — a multi-track mix you'd likely want to prune.

These gaps are filed as follow-up beads (see ["Detection gaps filed"](#detection-gaps-filed) below); for now, the generated track is usable as-is for a managed Expo app.

---

## Step 3: Dispatch a feature into dependency-ordered cars

We'll add a settings screen with a persisted theme toggle:

```console
$ ry car create --type epic --track frontend --title "Settings screen with persisted theme toggle"
Created car car-b9583

$ ry car create --parent car-b9583 --type task --title "Theme context with AsyncStorage persistence"
Created car car-d8f67
$ ry car create --parent car-b9583 --type task --title "Settings screen UI"
Created car car-897eb
$ ry car create --parent car-b9583 --type task --title "Wire theme toggle + jest tests"
Created car car-b6625

$ ry car dep add car-897eb --blocked-by car-d8f67   # screen needs the theme context
$ ry car dep add car-b6625 --blocked-by car-897eb   # wiring + tests come last

$ ry car publish car-b9583 --recursive
Published 4 car(s) starting from car-b9583
```

Only the foundational card is ready:

```console
$ ry car ready
ID         TITLE                                     TRACK     PRI
car-d8f67  Theme context with AsyncStorage persi...  frontend  2
```

---

## Step 4: Run engines with jest as the merge gate

> ⚠️ **Live engines below.** `ry start` spawns AI sessions (real API usage). Commands are exact; behavior described is from Railyard's design, not captured here.

```bash
ry start --engines 2
tmux attach -t railyard
```

- Engines work the theme-context → settings-UI → wiring chain in dependency order on `ry/yourname/frontend/<car-id>` branches.
- **Yardmaster runs `npm test` (jest)** on each completed branch and merges only if it passes.

### Verifying UI work without a device

There's no browser or device in the loop, so engines verify UI-adjacent work with **jest + `@testing-library/react-native`** — rendering components, asserting on the rendered tree, and firing events. That's the evidence story for native mobile.

**Playwright does not apply here.** The [Playwright PR Demo](playwright-pr-demo.md) feature drives a real browser and is for web frontends only — it has no equivalent for native React Native rendering. Don't enable a `playwright:` block on a React Native track; rely on jest/RNTL (and, for full end-to-end, a device-farm tool like Detox or Maestro, which Railyard does not orchestrate).

---

## Other mobile stacks (Flutter, Swift, Kotlin)

Railyard detects these and generates a `mobile` track for each — use this tutorial's flow, swapping the stack:

| Stack | Detected from | Track | Default `test_command` |
|---|---|---|---|
| **Flutter** | `pubspec.yaml` with a `flutter:` dependency | `mobile` (dart) | `flutter test` |
| **Dart (pure)** | `pubspec.yaml`, no Flutter | `mobile` (dart) | `dart test` |
| **Swift** | `Package.swift` or `*.xcodeproj`/`*.xcworkspace` | `mobile` (swift) | `swift test` |
| **Kotlin / Android** | `AndroidManifest.xml` (app/, androidApp/, android/app/) | `mobile` (kotlin) | `./gradlew test` |

A full walkthrough for these stacks isn't included here (React Native was chosen to keep prerequisites near-zero); the detection and presets above are the starting point. If you want a dedicated Flutter or native walkthrough, open an issue.

---

## Detection gaps filed

While writing this tutorial, the following React Native detection gaps were filed as follow-up work (linked from bead railyard-a37.8):

- React Native / Expo repos are classified as a generic `typescript` `frontend` track with no RN-specific preset or `mobile` classification — consider an RN-aware preset.
- A bare (ejected) RN/Expo repo additionally emits Kotlin + Swift tracks from its `android/`/`ios/` native dirs, producing a confusing multi-track mix that likely needs pruning.

---

## Troubleshooting

- **`ry init` detects extra `mobile` tracks** — you're on a bare/ejected RN app with native dirs; remove the Swift/Kotlin tracks from `railyard.yaml` if you only want the RN/jest track.
- **`npm test` fails immediately** — ensure a jest preset is configured (`jest-expo`) and `@testing-library/react-native` is installed.
- **Engines stall on native builds** — keep engine work to the JS/TS layer + jest; native device builds are out of scope for the core loop.

---

See also: [README Quickstart](../README.md#quickstart) · [JS game tutorial](tutorial-js-game.md) · [Go service tutorial](tutorial-go.md) · [Laravel tutorial](tutorial-laravel.md)
