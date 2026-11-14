# Dashboard help

The dashboard is the front page of Capstan. It lists your stacks and shows what
needs attention. Open a view from the tab row (Stacks, Host, Directories,
Updates, Backups). The **Host** tab covers the server itself: live load, disk use,
and containers, images, volumes, networks and build cache. See
[Docker host help](docker-host.md).

## Stacks

The stack list is the default view. Each row shows the stack name, its status,
how many containers it has, and the auto-update and backup switches. Stacks are
grouped by the directory they live in, and a branch icon marks stacks that are
git repositories. Above the list, a strip appears only when something needs a
look: pending image updates, stacks in error, or stopped stacks.

**Common tasks**

- **Open a stack:** click its row to reach the stack page.
- **Start, stop or restart:** use the icons at the end of the row. A running stack
  offers Stop and Restart, any other stack offers Start.
- **Create a stack:** use **New Stack** at the top right.
- **Find a stack:** type in the filter box, narrow by status (All, Running,
  Stopped, Paused, Error) and sort by name or status.
- **Turn on auto-update or backup for a stack:** use the switches in its row.
  Both are explained in [Stack page help](stack-page.md#overview).
- **Delete a stack:** open the **...** menu on the row and choose Delete.

**Gotchas**

- **Delete removes data.** It removes the stack and everything in its directory,
  including its compose file and any data stored there. It cannot be undone.
- **A stale status shows a notice.** If Docker could not be read, the list keeps
  the last known status and says it may be out of date. Use Retry on the notice.
- **Auto-update needs the master switch.** A row's auto-update switch stays locked
  until auto-update is enabled in Settings, Updates. See
  [Settings help](settings.md#scan-interval-and-auto-update).

## Host

The Host tab shows how busy the server is and what fills its disk. It has its
own help: see [Docker host help](docker-host.md). A size beside the tab name is
how much unused Docker data one click on **Free up** would remove.

## Directories

The folders Capstan watches for stacks, shown as a tree under each configured
root. The number beside a folder counts the stacks inside it, and a branch badge
shows the checked-out branch for git repositories.

**Common tasks**

- **Jump to a stack:** click a folder that holds a stack.
- **Collapse a branch of the tree:** use the arrow beside the folder. Capstan
  remembers which folders you collapsed in this browser.
- **Add or change roots:** see [Settings help](settings.md#directories).

**Gotchas**

- **This view is read-only.** Roots are added through the environment and
  Settings, not here.
- **New folders need a rescan.** A compose file deeper than the scan depth does
  not appear until you raise the depth and rescan.

## Updates

The result of the last image scan. A row means a newer image was published for the
tag a container is running. Each row shows the container, its image, the stack it
belongs to and whether it is running.

**Common tasks**

- **Check now:** use **Check for Updates**. The page shows when the last scan ran.
- **Update one container:** use the update action on its row. Capstan pulls the new
  image and recreates the container.
- **Read the output:** expand a row to see the progress and result of its update.
- **Automate it:** turn on auto-update per container or stack, after enabling the
  master switch in Settings, Updates.

**Gotchas**

- **Updating restarts the container.** Expect a short interruption.
- **Check the scan time before trusting a short list.** If a refresh fails, the
  page keeps the earlier results and shows a notice. **Last scanned** tells you how
  old they are.
- **Scanning is off until you set an interval.** Set one in Settings, Updates.

## Backups

The history of every backup operation: scheduled and manual backups, cloud syncs,
restores, prunes and repository checks. Each run lists its status, trigger and how
much new data it wrote. Expand a run to see the result for each stack.

**Common tasks**

- **Find a failed run:** filter by status (Failed, Partial, Skipped, Interrupted),
  by kind, by trigger (Manual or Scheduled) or by date range.
- **Start a backup or a check:** use the backup card in Settings, Backup.
- **Restore a stack:** open the stack, then Activity and Backups. See
  [Restoring a backup](../how-to/restore-a-backup.md#restore).

**Gotchas**

- **Partial is not success.** A run that saved every stack but could not capture
  the Capstan database is reported as partial.
- **Skipped means it never started.** A scheduled backup that falls due while
  another operation holds the repository is skipped, not queued.
- **"No change" is normal.** Backups are deduplicated, so an unchanged stack adds
  little or nothing.
- **Setup lives elsewhere.** Repository, schedule and retention are configured in
  [Settings help](settings.md#backup), and the guide is
  [Configuring backups](../how-to/configure-backups.md).

---

[← Documentation index](../../README.md#documentation)
