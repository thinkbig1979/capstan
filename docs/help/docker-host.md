# Docker host help

The Docker host page shows what Docker keeps on disk on the server Capstan runs
on: how much is in use, how much could be removed, and a list for each kind of
resource. Open it from the **Host** strip under the stack list on the dashboard.
The numbers refresh every minute and when Docker reports a change.

## Overview

The top of the page is a disk bar with Docker's total disk use, split into space in
use and space that could be reclaimed. Below it, one card per kind of resource shows
a count or a size and a line of detail. A card also shows what could be removed, or
"Nothing to clean up". **View all** opens that resource's list.

**Common tasks**

- **Find what is using space:** compare the cards, then open the biggest with **View all**.
- **Switch between lists:** use the Containers, Images, Volumes, Networks and
  Build cache links at the top of any list.

**Gotchas**

- **Volume sizes can read "size unknown".** Docker could not measure every volume, so
  the totals count only the volumes it could. Unknown is not zero.
- **A card reports what could be removed, it removes nothing.** Removing happens in
  each list, with **Prune** or the row icons.
- **If a refresh fails,** the page keeps the last numbers and says the refresh failed.
  If the first load fails, it shows no numbers and offers **Retry**.

## Automatic cleanup

Capstan can remove dangling images and old build cache on a schedule. It is off until
you turn it on. You set it in **Settings > Docker cleanup**: the schedule switch, the
age floor in hours, how often it runs, a preview of what a run would remove, and a
table of past runs. See [Settings help](settings.md#docker-cleanup).

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
- **Size can read "size unknown".** That means Docker gave no figure for the
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
