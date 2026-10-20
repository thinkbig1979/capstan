# Stack page help

The stack page is where you work on one compose project: see its services, start
and stop it, edit its files, read its logs, open a shell and review what happened
to it. The header holds the lifecycle buttons (Start, Stop, Restart, Pull Images),
and the tabs below hold everything else.

## Overview

The first tab shows the stack's **Services** (one row per container, with shortcuts
to its logs and a shell) and a side panel with the compose file, env file, service
count and, for git stacks, the branch. The panel also holds the **Auto-update** and
**Backup** switches.

**Common tasks**

- **Start, stop or restart the stack:** use the header buttons. Stop takes the
  whole project down, and Restart stops it and starts it again.
- **Pull Images:** download the newest image for each service. Running containers
  keep the old image until you restart, so this only stages the update.
- **Jump to a service's logs or shell:** use the shortcuts on its row.
- **Delete the stack:** open the **...** menu in the header, choose Delete Stack,
  and type the stack name to confirm.

**Gotchas**

- **Delete removes the stack folder.** That includes the compose file and any data
  stored in the folder. It cannot be undone.
- **Edits apply on the next start.** Saving a compose or env file does not touch the
  running containers. Restart the stack to apply the change.
- **A stale status shows a notice.** If Docker could not be read, the page keeps the
  last known status and says it may be out of date.

### Backup and stop policy

The **Backup** switch adds this stack to scheduled backups. Once it is on, a second
control appears beside it that decides what happens to the stack while it is copied.
A stack backup covers the stack's folder: the compose file, the env file and any
files stored inside it. Set up the repository and schedule under Settings, Backup.

**Common tasks**

- **Turn backups on or off:** use the Backup switch. It needs a working repository,
  so set one up first in [Settings help](settings.md#backup).
- **Choose a stop policy:** use the dropdown that appears when Backup is on.
- **Restore a snapshot:** open Activity, then Backups. See
  [Restoring a backup](../how-to/restore-a-backup.md#restore).

**Gotchas**

- **Stop during backup (default)** stops the stack, copies its folder and starts it
  again afterwards. The copy is a consistent point in time, at the price of a short
  outage. A stack that was already stopped stays stopped. If the backup fails, Capstan
  still tries to start the stack again.
- **Back up live** copies the folder while the stack keeps running. There is no
  downtime, but a file being written during the copy, such as a database, can be
  captured half-written. Use it for stacks with no live databases, or accept that
  risk.
- **The policy only applies to backups.** A restore always stops the stack first,
  whichever policy is set.
- **Named Docker volumes are not in the copy.** Data kept in a bind mount inside the
  stack folder is. See [what gets backed up](../how-to/configure-backups.md#what-gets-backed-up).
- **The last-run icon is install-wide.** It reports the most recent backup operation
  of any stack, not only this one.

### Auto-update

The **Auto-update** switch lets this stack update itself whenever a scan finds a
newer image. It pulls the image and recreates the containers.

**Common tasks**

- **Opt a stack in:** turn the switch on. Check Settings, Updates for the scan
  interval and the schedule, see [Settings help](settings.md#scan-interval-and-auto-update).
- **Opt a single container in or out:** use the Auto-Update column on the dashboard
  Containers view. Container settings override the stack switch, and the panel shows an
  info icon when any exist.

**Gotchas**

- **The master switch comes first.** While global auto-update is off in Settings, the
  stack switch is locked.
- **Updates cause a short outage** while containers are recreated.
- **Three failures in a row pause it.** The switch then shows as off, with a tooltip
  saying why. Check the Updates section under Activity for the cause. Before that, a
  small "1f" or "2f" beside the switch counts the failures so far.

## Editor

Two editors side by side: the compose file and the environment (.env) file. Use it
for small changes without leaving the browser.

**Common tasks**

- **Save:** press Save or Ctrl+S. Capstan validates the compose file first.
- **Check without saving:** press Lint to see warnings and errors.
- **Move a value into .env:** select it in the compose file and use **Extract to
  .env**, then reference it as a variable.
- **Edit variables as a table or as raw text:** switch between Table View and Raw
  Editor. Undo and Redo work in the env editor.
- **Read a hidden value:** keys that look like secrets are masked. Unlock them with
  your password to reveal them for 5 minutes. You can lock them again at any time.

**Gotchas**

- **Saving does not restart anything.** Restart the stack to apply the change.
- **Lint errors block saving.** The server refuses a compose file with errors, so fix
  them first. Warnings still save.
- **Unsaved edits are marked with a badge.** Save them before you leave the page.
- **Raw Editor is hidden while values are locked,** because a masked file would look
  empty and invite an overwrite.
- **Variables shared by every stack live in Settings.** See
  [Settings help](settings.md#global-environment-variables).

## Logs

A live stream of the stack's container output, merged and colour-coded by container.

**Common tasks**

- **Search:** press `/` to focus the search box, or click it.
- **Narrow to one container:** use the container filter.
- **Narrow by time:** pick All, Last 5 min, Last 15 min, Last 1 hr or Custom.
- **Read comfortably:** toggle timestamps and line wrapping.
- **Keep a copy:** download the logs, or clear the view.

**Gotchas**

- **The view keeps the latest 10,000 lines.** Older lines drop off. Download before
  they do if you need them.
- **Clear empties the view,** not the container's own log.
- **A stopped stack has no live logs.** Start it to see output.

## Terminal

An interactive shell inside one of the stack's running containers, in the browser.

**Common tasks**

- **Open a shell:** pick the container from the dropdown. The Shell link on an
  Overview row preselects it.
- **Copy and paste:** Ctrl+Shift+C and Ctrl+Shift+V.
- **Search the output:** Ctrl+Shift+F.
- **Change the font size** or **disconnect** from the toolbar.

**Gotchas**

- **It needs a running container.** With the stack stopped the tab shows
  "No running containers".
- **Idle sessions end.** After 25 minutes without typing you get a warning, then a
  grace period and a 60 second countdown, then the session disconnects.
- **You are inside the container.** Changes to its filesystem vanish when the
  container is recreated, unless they are in a mounted volume.
- **Shell access is powerful.** Anyone who can sign in to Capstan can run commands
  here. See the [security model](../explanation/security-model.md).

## Metrics

Live CPU, memory, network and disk I/O for the containers in this stack.

**Common tasks**

- **Find the container that is eating memory:** compare the per-container rows.
- **Watch the effect of a change:** keep the tab open while you restart or update.

**Gotchas**

- **Numbers are live, not stored.** They start when you open the tab and are gone when
  you leave. There is no history.
- **Stopped containers show nothing.** There is no stream for them.
- **If the connection drops,** the tab says so and retries on its own a few times.
  Reload the page if it gives up.

## Activity

What has happened to this stack, in three sections: **Updates**, **Backups** and
**Git History**.

**Common tasks**

- **See past updates:** open Updates and filter by status or trigger.
- **Browse snapshots and restore one:** open Backups. Preview lists the files in a
  snapshot before you restore it.
- **Review commits:** open Git History for stacks that are git repositories.

**Gotchas**

- **Restore replaces the folder.** The stack is stopped, its directory is replaced by
  the snapshot, and files created since the snapshot are deleted. Mounted filesystems
  and cache directories inside it are left alone. A failed restore leaves the stack
  stopped so you can inspect it.
- **Git History needs a git stack.** Other stacks show a message instead.
- **Backups appears empty until backups run.** Enable Backup on the Overview tab and
  check Settings, Backup.

---

[← Documentation index](../../README.md#documentation)
