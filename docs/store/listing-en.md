# Connect IQ Store listing — English

**Name:** AI Limits

**Short description (≤ 80):** Your Claude Code usage limits and reset times in the widget loop.

**Description:**

AI Limits shows your Claude Code subscription limits on your watch, the way the Limits app does on a phone:

• the 5-hour session
• the weekly limit
• the weekly limit of a specific model, when your plan has one

Each limit shows how much you have used and when it resets. A glance in the widget loop gives you all three at a glance; press START for the full screen.

What you need
• Claude Code signed in with a Pro or Max subscription on your computer
• the free AI Limits helper on that computer (macOS, Windows, Linux): github.com/SecondBrainProd/ai-limits
• your phone nearby with Garmin Connect (the watch reaches the internet through it)

Setup takes a minute: the watch shows a six-character code, you run `ailimits pair CODE` on your computer, then `ailimits install-service`.

Privacy
Your Claude token stays on your computer. The helper sends only percentages, reset times and model names to the relay that serves the watch. No names, no e-mail, no account data. Details: ailimits.second-brain.ru

Unofficial. Uses your local Claude Code token only to read your usage limits; the token never leaves your computer. May stop working if Anthropic changes its API. Not affiliated with Anthropic.

**Category:** Widgets → Productivity

**Links:** Website / support — https://github.com/SecondBrainProd/ai-limits · Privacy policy — https://ailimits.second-brain.ru

**What's new (0.1.0):** First release: fēnix 5–9, fēnix E, Chronos, MARQ, MARQ 2 and MARQ 3.

**Screenshots:** `docs/store/screenshots/` — fēnix 7 (260 px), fēnix 8 (454 px), MARQ 2 (390 px): glance, full screen, pairing.

**Package:** `watch/bin/AILimits.iq` (built by `tools/build-watch.sh all -e`; signed with the developer key — keep the same key for every update).
