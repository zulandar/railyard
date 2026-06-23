# Tutorial: Build a Mobile App (React Native / Expo) with Railyard

Mobile is the stretch case for "any language." This tutorial uses **React Native via Expo** — it keeps prerequisites near-zero (no Xcode/Android Studio needed for the core loop) and stays in the JavaScript/TypeScript ecosystem most readers know. It also doubles as the **landing page for mobile**: Flutter, Swift, and Kotlin all have detection presets, pointed to at the end.

**What you'll learn:**
- What `ry init` actually detects on an Expo/React Native repo — it now generates a **mobile-aware** track (named `mobile`), not a generic web frontend
- Running a feature through dependency-ordered cars with **jest** as the merge gate
- How engines verify UI-adjacent work without a device, and why Playwright does **not** apply to native mobile

**Time:** ~10 minutes of setup, then the engines build.

> **What's exact vs. described:** the `ry init` commands and the generated `railyard.yaml` below match a **real run** of `ry init` on a minimal Expo-shaped project (the detection-relevant files: `package.json` with `expo`/`react-native`, `tsconfig.json`, `app.json`, an `app/index.tsx`). A full `npx create-expo-app` produces more files but the **detection result is the same**. Steps that spawn live AI engines are marked; those commands are exact, but their behavior is described from Railyard's design rather than captured — no agent output is fabricated here.

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

## Step 2: `ry init` — what gets detected

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
  - name: mobile
    language: typescript
    file_patterns: ["**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx"]
    engine_slots: 2
    test_command: "npx jest"
```

**What this gets right:** the `tsconfig.json` makes it a TypeScript track, and the patterns include `**/*.tsx` — so your screens and components are routed correctly. Because `ry init` recognizes the `expo` dependency (or a top-level `expo` key in `app.json`), it applies the **Expo preset**: the track is named **`mobile`** rather than `frontend`, and engine prompts get an Expo/React-Native convention (use `npx expo`, treat this as a React Native app — not a web frontend). The track is **mobile-aware**, not a generic web frontend.

**About the test command:** with `jest-expo` in your `devDependencies`, the preset sets `test_command` to **`npx jest`** (a fresh Expo app doesn't always wire a `test` npm script, so init invokes jest directly rather than assuming `npm test` exists). If `jest-expo` is **not** present, the track keeps the default `test_command: "npm test"` — add a jest preset (see Troubleshooting) so the merge gate has a real suite to run.

> **Note on conventions.** The Expo convention the preset injects guides the engines' prompts but is **not** written into `railyard.yaml` — `ry init` only emits `name`, `language`, `file_patterns`, `engine_slots`, and `test_command`. So the generated file shows the `mobile` track name and `npx jest` gate, not a `conventions:` block.

**Managed vs. bare/ejected:**
- A **managed** Expo app has no native `android/`/`ios/` directories, so only the TypeScript signal fires — you get the single `mobile` track above.
- A **bare** RN/Expo app that has ejected/prebuilt to native code *does* have `android/` and `ios/` directories, which would normally trip the Kotlin (AndroidManifest) and Swift (`ios/*.xcodeproj`) markers. Railyard now **suppresses** those generated-native tracks when it sees a `react-native`/`expo` dependency, so you still get a single `mobile` (typescript) track — not a confusing kotlin+swift+typescript mix. (For a bare, non-Expo RN repo the preset is the React Native variant: same `mobile` track, and `test_command` becomes `npx jest` when `jest` is in `devDependencies`.)

---

## Step 3: Dispatch a feature into dependency-ordered cars

We'll add a settings screen with a persisted theme toggle:

```console
$ ry car create --type epic --track mobile --title "Settings screen with persisted theme toggle"
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
ID         TITLE                                     TRACK   PRI
car-d8f67  Theme context with AsyncStorage persi...  mobile  2
```

---

## Step 4: Run engines with jest as the merge gate

> ⚠️ **Live engines below.** `ry start` spawns AI coding sessions that consume real API usage. Commands are exact; the behavior described is from Railyard's design, not captured here.

```bash
ry start --engines 2
tmux attach -t railyard
```

- Engines work the theme-context → settings-UI → wiring chain in dependency order on `ry/yourname/mobile/<car-id>` branches.
- **Yardmaster runs the track's `test_command` (here, `npx jest`)** on each completed branch and merges only if it passes. (If your repo has no `jest-expo` preset, the track's gate is `npm test` instead — wire one up so the gate actually runs your suite.)

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

## How RN/Expo detection works today

An earlier version of this tutorial flagged two React Native detection gaps as follow-up work. Both are now **resolved**, and the behavior described above is the result:

- **RN/Expo no longer falls back to a generic `frontend` track.** When `ry init` sees an `expo` dependency (or an `expo` key in `app.json`) it applies the **Expo preset**; a bare `react-native` dependency applies the **React Native preset**. Either way the track is named `mobile`, carries a React-Native/Expo convention for the engines, and uses `npx jest` as its gate when the matching jest preset (`jest-expo` for Expo, `jest` for bare RN) is installed.
- **Bare/ejected repos no longer emit phantom kotlin + swift tracks.** The generated `android/` and `ios/` native directories would otherwise trip the Android (`AndroidManifest.xml`) and Swift (`*.xcodeproj`) markers, but Railyard now suppresses those generated-native tracks whenever a `react-native`/`expo` dependency is present, leaving a single `mobile` (typescript) track.

Hand-authored native apps and Flutter projects (which carry no `react-native`/`expo` dependency) are unaffected — they keep their own `mobile` tracks, covered next.

---

## Troubleshooting

- **Track is named `frontend`, not `mobile`** — the Expo/RN preset only fires when `ry init` sees an `expo` dependency (or `app.json` `expo` key) or a `react-native` dependency. If your `package.json` doesn't declare one (or `app.json` lacks the `expo` key), detection treats the repo as a plain Node/TypeScript project. Add the dependency and re-run, or rename the track to `mobile` in `railyard.yaml`.
- **`test_command` is `npm test`, not `npx jest`** — the preset only switches to `npx jest` when the matching jest preset (`jest-expo` for Expo, `jest` for bare RN) is in `devDependencies`. Install it and re-run `ry init`, or edit `test_command` directly.
- **The merge gate fails immediately** — ensure a jest preset is configured (`jest-expo`) and `@testing-library/react-native` is installed so the suite actually runs.
- **A bare/ejected RN repo still emits Swift/Kotlin tracks** — this shouldn't happen on current Railyard (generated-native tracks are suppressed when a `react-native`/`expo` dependency is present). If it does, confirm the dependency is declared in `package.json`; otherwise prune the extra tracks from `railyard.yaml`.
- **Engines stall on native builds** — keep engine work to the JS/TS layer + jest; native device builds are out of scope for the core loop.

---

See also: [README Quickstart](../README.md#quickstart) · [JS game tutorial](tutorial-js-game.md) · [Go service tutorial](tutorial-go.md) · [Laravel tutorial](tutorial-laravel.md)
