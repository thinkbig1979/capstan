# Dashboard help

The dashboard is the front page of Capstan. It lists your stacks and the Docker
resources on the host, and each view below answers one question: what is
running, what needs attention, and what is taking up space. Open a view from the
tab row (Stacks, Metrics, Directories, Updates, Backups) or from the **Host**
strip under the stack list (Containers, Images, Volumes, Networks, Build cache).

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

## Metrics

Live counters for the whole host: how many stacks and containers exist and are
running, a Performance card (memory, network I/O and process count), disk use by
type, and a small trend chart for each container.

**Common tasks**

- **Spot a heavy container:** scan the Container Metrics charts.
- **Check disk pressure:** read the Disk Usage card, then open Images, Volumes or
  Build cache to see what is using it.

**Gotchas**

- **The numbers are live, not stored.** Trends start when you open the page and
  reset when you leave. Capstan does not keep metric history.
- **The numbers need the live connection.** If it drops, the cards stop updating
  until it returns.
- **Disk usage can read "unavailable".** That means Docker could not answer, not
  that the disks are empty.

## Containers

Every container on the host, split into **Stack containers** (started by a
Capstan-managed compose project) and **Standalone containers** (started some other
way). Each row shows memory, network, disk and uptime, restart count and
auto-update state.

**Common tasks**

- **Inspect a container:** use the inspect icon for its configuration and details.
- **Start, stop, restart or pull:** use the row icons. Pull downloads the newest
  image without restarting anything.
- **Clear stopped containers:** use **Prune**.

**Gotchas**

- **Remove and Prune are permanent.** Removing a container deletes it. Prune
  removes all stopped containers on the host, including ones outside Capstan.
- **Stopped containers have no live stats.** Their CPU and memory cells stay empty.
- **Prefer the stack page for stack containers.** Acting on one container of a
  compose project works, but a stack-level start or restart keeps the whole
  project consistent.

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

## Images

The Docker images stored on the host, with size, age and how many containers use
each one. The page also shows how much a scheduled cleanup would reclaim.

**Common tasks**

- **Find large images:** sort by size.
- **Remove one image:** use the remove icon on its row.
- **Reclaim space in bulk:** use **Prune**. By default it removes dangling images,
  the untagged leftovers of rebuilds. Enable the "all unused" option to remove every
  image that no container uses, and set an age to spare recent ones.
- **Schedule it:** follow the Cleanup settings link. See
  [Settings help](settings.md#docker-cleanup).

**Gotchas**

- **"All unused" is broad.** It also removes images you pulled for a stack that is
  currently stopped. They download again on the next start.
- **An image a container still uses is not pruned.** Remove or stop that container
  first.

## Volumes

Docker volumes: the storage containers use for data that must outlive them, such as
databases. Each row shows the driver, size, the stack that uses it and where it lives
on disk.

**Common tasks**

- **See what a stack stores:** read the Stack column.
- **Spot unused volumes:** a volume with no container attached is unused.
- **Remove a volume:** use the remove icon, or **Prune** for all unused volumes.

**Gotchas**

- **Removing a volume deletes its data for good.** There is no undo.
- **Size can read "not calculated".** That means Docker gave no figure for the
  volume, not that it is empty.
- **Capstan backups copy the stack directory.** A named Docker volume is not part of
  that copy. See [what gets backed up](../how-to/configure-backups.md#what-gets-backed-up).

## Networks

Private Docker networks that let the containers of a stack reach each other. Each
stack gets its own. Rows show the driver, scope, whether the network is internal,
how many containers are attached and the stack it belongs to.

**Common tasks**

- **Create a network:** use **Create** when containers from several stacks need to
  share one.
- **Remove unused networks:** use **Prune**.

**Gotchas**

- **Built-in networks stay.** The bridge, host and none networks cannot be removed.
- **A network in use will not delete.** Detach or stop its containers first.

## Build cache

Layers Docker saved while building images, so repeat builds run faster. Rows show the
type, a description, size, when the entry was last used and whether a build is using it.

**Common tasks**

- **Free disk space:** use **Prune**. Set an age to keep recent entries.
- **Find the biggest entries:** sort by size.

**Gotchas**

- **Clearing it is safe, but the next build is slower.** Docker rebuilds whatever it
  needs.
- **The "all" option also drops reusable cache.** The default removes only entries
  that nothing is using.

---

[← Documentation index](../../README.md#documentation)
