# Backup recording

Record on Oct 27–28 so there's a video to switch to if the venue network or
the laptop misbehaves. Keep it under 5 minutes, the same steps as live.

## Before you record

- `make up && make preflight`: ends with `Ready.`
- Turn off notifications (Focus), hide the Dock and menu bar extras, close
  everything that could pop up. Turn off any TLS-inspecting proxy.
- One terminal window, 1920×1080, dark background, font 20–24 pt, about 100×30
  characters. Run `clear` and set a short prompt (`PS1='$ '`).
- Nothing on screen should show `anthropic.env`, the Console's IDs, or a full
  token. The steps never print one.

## Recording

1. Start the screen recording: macOS **⌘⇧5 → Record Selected Portion**, around
   the terminal, or QuickTime. Microphone off; narrate live over it.
2. `make demo`. It waits for a key before every command, so you set the pace.
   Leave about two seconds on each result before the next key.
3. Stop the recording after `Nothing to steal.`

Then do a second take straight away (`make reset && make demo`), so you have a
spare.

## After recording

- Watch both takes end to end. Check the 401 in step 2, Claude's answer in
  step 4, and that no IDs or tokens are readable when paused.
- Trim the start and end (QuickTime: **Edit → Trim**). Export 1080p MP4.
- Keep copies on the laptop's desktop and a USB stick, and put the link or
  file name on the `demo-steps` slide's "backup recording" placeholder.
- On stage, play it from the local file, not from a streaming link.

## Dry run (Oct 29)

`make preflight && make demo` on the presentation laptop, on the
network you'll have at the venue if possible, with the projector resolution.
Time it: the five steps should take about 3–4 minutes live.
