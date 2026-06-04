import { useState, useMemo, useCallback, useRef, useEffect } from "react";
import { Link, useLocation } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useUIStore } from "@/stores/uiStore";
import { stacksApi, settingsApi, resourcesApi, backupApi } from "@/lib/api";
import {
  Search,
  X,
  ChevronDown,
  ChevronRight,
  ArrowUpDown,
  Boxes,
  FolderOpen,
  PanelLeftOpen,
  PanelLeftClose,
  LayoutDashboard,
  Settings,
  Star,
  ListChecks,
  Play,
  Square as SquareIcon,
  CheckSquare,
  RotateCcw,
  ArrowDownToLine,
  Database,
  ArrowUpCircle,
} from "lucide-react";
import {
  buildDirectoryTree,
  countTreeNodeStacks,
  hasTreeNesting,
  type TreeNode,
} from "@/lib/stack-tree";
import type { Stack, StackStatus } from "@/types";

// Filled status dot per stack status (a pip, not the old outlined Square icon which read as a
// selection checkbox). Vocabulary matches the Running/Stopped/Error legend.
const statusDotColor: Record<StackStatus, string> = {
  running: "bg-success",
  partial: "bg-warning",
  stopped: "bg-muted-foreground",
  error: "bg-destructive",
  unknown: "bg-muted-foreground",
};

type BulkAction = "start" | "stop" | "restart" | "pull";

const BULK_LABELS: Record<BulkAction, string> = {
  start: "Started",
  stop: "Stopped",
  restart: "Restarted",
  pull: "Pulled",
};

// Compact "x ago" for a past timestamp.
function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.round(diff / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.round(hrs / 24)}d ago`;
}

// Compact "in x" for a future timestamp.
function untilTime(iso: string): string {
  const diff = new Date(iso).getTime() - Date.now();
  if (diff <= 0) return "due";
  const mins = Math.round(diff / 60000);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h ${mins % 60}m`;
}

function loadCollapsed(): Set<string> {
  try {
    const raw = localStorage.getItem("sidebar-collapsed");
    if (raw) return new Set(JSON.parse(raw));
  } catch {
    /* ignore */
  }
  return new Set();
}

function saveCollapsed(set: Set<string>) {
  localStorage.setItem("sidebar-collapsed", JSON.stringify([...set]));
}

export function Sidebar() {
  const sidebarOpen = useUIStore((s) => s.sidebarOpen);
  const toggleSidebar = useUIStore((s) => s.toggleSidebar);
  const closeSidebar = useUIStore((s) => s.closeSidebar);
  const sidebarWidth = useUIStore((s) => s.sidebarWidth);
  const setSidebarWidth = useUIStore((s) => s.setSidebarWidth);
  const pinnedStacks = useUIStore((s) => s.pinnedStacks);
  const togglePinnedStack = useUIStore((s) => s.togglePinnedStack);
  const location = useLocation();
  const queryClient = useQueryClient();
  const isDragging = useRef(false);
  const dragWidthRef = useRef(0);

  const [searchQuery, setSearchQuery] = useState(
    () => localStorage.getItem("sidebar-search") || "",
  );
  const [statusFilter, setStatusFilter] = useState<StackStatus | "all">(() => {
    const saved = localStorage.getItem("sidebar-filter");
    return (saved as StackStatus | "all") || "all";
  });
  const [sortBy, setSortBy] = useState<"name" | "status">(() => {
    return (
      (localStorage.getItem("sidebar-sort") as "name" | "status") || "name"
    );
  });
  const [collapsedGroups, setCollapsedGroups] =
    useState<Set<string>>(loadCollapsed);

  // Bulk-selection mode: rows become checkboxes and a bulk action bar appears.
  const [selecting, setSelecting] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [bulkPending, setBulkPending] = useState(false);

  useEffect(() => {
    localStorage.setItem("sidebar-search", searchQuery);
  }, [searchQuery]);
  useEffect(() => {
    localStorage.setItem("sidebar-filter", statusFilter);
  }, [statusFilter]);
  useEffect(() => {
    localStorage.setItem("sidebar-sort", sortBy);
  }, [sortBy]);
  useEffect(() => {
    saveCollapsed(collapsedGroups);
  }, [collapsedGroups]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const mq = window.matchMedia("(max-width: 1023px)");
    if (mq.matches) {
      closeSidebar();
    }
  }, [location.pathname, closeSidebar]);

  const { data: stacks = [], isLoading } = useQuery({
    queryKey: ["stacks"],
    queryFn: () => stacksApi.list(),
    staleTime: 30_000,
  });

  const { data: config } = useQuery({
    queryKey: ["config"],
    queryFn: settingsApi.getConfig,
    staleTime: Infinity,
  });

  // Cached update scan — drives the aggregate "N updates" badge. refresh=false
  // never kicks off a heavy scan, it just reads whatever the backend has.
  // Shares the canonical ['resources','updates'] key so update mutations and the
  // scan watcher (useResources) invalidate this badge too — otherwise it stays
  // stale until a full page refresh.
  const { data: updateData } = useQuery({
    queryKey: ["resources", "updates"],
    queryFn: () => resourcesApi.checkUpdates(false),
    staleTime: 60_000,
    retry: false,
  });
  const updateCount = updateData?.updates?.length ?? 0;

  // Global backup status for the footer (last run + next-run countdown).
  const { data: backupStatus } = useQuery({
    queryKey: ["backup-status"],
    queryFn: backupApi.getStatus,
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: false,
  });

  const configuredDirs = useMemo(() => {
    if (!config?.stacksDirectories) return [];
    return config.stacksDirectories.map((p: string) => ({
      path: p,
      name: p.split("/").filter(Boolean).pop() || p,
    }));
  }, [config]);

  const filteredStacks = useMemo(() => {
    let result = [...stacks];
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      result = result.filter((s) => s.projectName.toLowerCase().includes(q));
    }
    if (statusFilter !== "all") {
      result = result.filter((s) => s.status === statusFilter);
    }
    result.sort((a, b) => {
      if (sortBy === "status")
        return (
          a.status.localeCompare(b.status) ||
          a.projectName.localeCompare(b.projectName)
        );
      return a.projectName.localeCompare(b.projectName);
    });
    return result;
  }, [stacks, searchQuery, statusFilter, sortBy]);

  const pinnedVisible = useMemo(
    () => filteredStacks.filter((s) => pinnedStacks.includes(s.id)),
    [filteredStacks, pinnedStacks],
  );

  const tree = useMemo(() => {
    if (configuredDirs.length === 0) return []
    return buildDirectoryTree(filteredStacks, configuredDirs.map((d) => d.path))
  }, [filteredStacks, configuredDirs])

  const treeByRoot = useMemo(() => {
    return configuredDirs
      .map((cd) => {
        const rootStacks = filteredStacks.filter(
          (s) => s.directory === cd.path || s.directory.startsWith(cd.path + '/'),
        )
        return {
          rootPath: cd.path,
          rootName: cd.name,
          nodes: buildDirectoryTree(rootStacks, [cd.path]),
        }
      })
      .filter((g) => g.nodes.length > 0)
  }, [filteredStacks, configuredDirs])

  const useGroups = useMemo(() => {
    if (stacks.length === 0) return false
    const allTree = buildDirectoryTree(stacks, configuredDirs.map((d) => d.path))
    return configuredDirs.length > 1 || allTree.length > 1 || hasTreeNesting(allTree)
  }, [stacks, configuredDirs])

  const toggleGroup = useCallback((dirPath: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(dirPath)) next.delete(dirPath);
      else next.add(dirPath);
      return next;
    });
  }, []);

  const toggleSelected = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const exitSelectMode = useCallback(() => {
    setSelecting(false);
    setSelectedIds(new Set());
  }, []);

  const selectAllVisible = useCallback(() => {
    setSelectedIds((prev) =>
      prev.size === filteredStacks.length
        ? new Set()
        : new Set(filteredStacks.map((s) => s.id)),
    );
  }, [filteredStacks]);

  const runBulk = useCallback(
    async (action: BulkAction) => {
      const ids = [...selectedIds];
      if (ids.length === 0) return;
      setBulkPending(true);
      const results = await Promise.allSettled(
        ids.map((id) => stacksApi[action](id)),
      );
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const failed = results.length - ok;
      const verb = BULK_LABELS[action];
      if (failed === 0) {
        toast.success(`${verb} ${ok} stack${ok === 1 ? "" : "s"}`);
      } else if (ok === 0) {
        toast.error(`Failed to ${action} ${failed} stack${failed === 1 ? "" : "s"}`);
      } else {
        toast.warning(`${verb} ${ok}, ${failed} failed`);
      }
      queryClient.invalidateQueries({ queryKey: ["stacks"] });
      queryClient.invalidateQueries({ queryKey: ["dashboard-stats"] });
      setBulkPending(false);
      exitSelectMode();
    },
    [selectedIds, queryClient, exitSelectMode],
  );

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      isDragging.current = true;
      dragWidthRef.current = sidebarWidth;
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      const startX = e.clientX;
      const onMove = (ev: MouseEvent) => {
        if (!isDragging.current) return;
        setSidebarWidth(sidebarWidth + ev.clientX - startX);
      };
      const onUp = () => {
        isDragging.current = false;
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
      };
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    },
    [sidebarWidth, setSidebarWidth],
  );

  const renderTreeNode = (node: TreeNode, depth: number, isFirst = false) => {
    const isCollapsed = collapsedGroups.has(node.fullPath);
    const totalStacks = countTreeNodeStacks(node);
    const isTopLevel = depth === 0;
    const indentPx = (2 + depth * 2) * 4;

    return (
      <div key={node.fullPath} className={isTopLevel && !isFirst ? 'border-t border-sidebar-border mt-1' : ''}>
        <button
          onClick={() => toggleGroup(node.fullPath)}
          className={`flex items-center gap-1.5 w-full transition-colors rounded ${
            isTopLevel
              ? 'py-1 text-[11px] font-semibold text-sidebar-accent-foreground bg-sidebar-accent/50 hover:bg-sidebar-accent/80'
              : 'py-0.5 text-[10px] font-medium text-muted-foreground hover:text-sidebar-foreground'
          }`}
          style={{ paddingLeft: `${indentPx}px`, paddingRight: '8px' }}
          title={node.fullPath}
        >
          {isCollapsed ? (
            <ChevronRight className={isTopLevel ? 'h-3 w-3 shrink-0' : 'h-2.5 w-2.5 shrink-0'} />
          ) : (
            <ChevronDown className={isTopLevel ? 'h-3 w-3 shrink-0' : 'h-2.5 w-2.5 shrink-0'} />
          )}
          {isTopLevel && <FolderOpen className="h-3.5 w-3.5 shrink-0" />}
          <span className="truncate flex-1 text-left">{node.name}</span>
          <span className={`font-normal tabular-nums ${isTopLevel ? 'text-[10px]' : 'text-[9px]'}`}>
            {totalStacks}
          </span>
        </button>
        {!isCollapsed && (
          <>
            {node.stacks.map(renderStack)}
            {node.children.map((child, i) => renderTreeNode(child, depth + 1, i === 0))}
          </>
        )}
      </div>
    );
  };

  const renderStack = (stack: Stack) => {
    const dotColor = statusDotColor[stack.status] || statusDotColor.unknown;
    const pinned = pinnedStacks.includes(stack.id);

    // Selection mode: a non-navigating row with a checkbox.
    if (selecting) {
      const checked = selectedIds.has(stack.id);
      return (
        <button
          key={stack.id}
          type="button"
          onClick={() => toggleSelected(stack.id)}
          aria-pressed={checked}
          className={`flex items-center gap-2 px-3 py-1.5 rounded text-sm w-full text-left transition-colors ${
            checked
              ? "bg-sidebar-accent text-sidebar-accent-foreground"
              : "hover:bg-sidebar-accent/50 text-sidebar-foreground"
          }`}
        >
          {checked ? (
            <CheckSquare className="h-4 w-4 shrink-0 text-primary" />
          ) : (
            <SquareIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
          )}
          <span className={`h-2 w-2 rounded-full shrink-0 ${dotColor}`} aria-hidden="true" />
          <span className="flex-1 truncate">{stack.projectName}</span>
        </button>
      );
    }

    const isActive = location.pathname.startsWith(`/stacks/${stack.id}`);
    return (
      <Link
        key={stack.id}
        to={`/stacks/${stack.id}`}
        className={`group flex items-center gap-2 px-3 py-1.5 rounded text-sm transition-colors ${
          isActive
            ? "bg-sidebar-accent text-sidebar-accent-foreground font-medium"
            : "hover:bg-sidebar-accent/50 text-sidebar-foreground"
        }`}
        aria-label={`${stack.projectName} - ${stack.status}`}
      >
        <span className={`h-2 w-2 rounded-full shrink-0 ${dotColor}`} aria-hidden="true" />
        <span className="flex-1 truncate">{stack.projectName}</span>
        {!!stack.containers?.length && (
          <Badge
            variant="secondary"
            className="h-4 min-w-5 px-1 text-[10px] leading-none"
          >
            {stack.containers.length}
          </Badge>
        )}
        {stack.isGitRepo && stack.gitDirty && (
          <span
            className="h-1.5 w-1.5 rounded-full bg-warning shrink-0"
            title="Uncommitted changes"
          />
        )}
        <span
          role="button"
          tabIndex={0}
          aria-label={pinned ? `Unpin ${stack.projectName}` : `Pin ${stack.projectName}`}
          title={pinned ? "Unpin" : "Pin to top"}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            togglePinnedStack(stack.id);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              e.stopPropagation();
              togglePinnedStack(stack.id);
            }
          }}
          className={`shrink-0 rounded p-0.5 hover:text-warning transition-opacity ${
            pinned
              ? "text-warning opacity-100"
              : "text-muted-foreground opacity-0 group-hover:opacity-100 focus:opacity-100"
          }`}
        >
          <Star className={`h-3 w-3 ${pinned ? "fill-current" : ""}`} />
        </span>
      </Link>
    );
  };

  if (!sidebarOpen) {
    const isDashboard = location.pathname === "/";
    const isSettings = location.pathname.startsWith("/settings");
    const stackCount = stacks.length;
    return (
      <aside
        aria-label="Collapsed navigation rail"
        className="hidden md:flex flex-col items-center w-14 shrink-0 border-r border-sidebar-border bg-sidebar text-sidebar-foreground py-2 gap-1"
      >
        <Button
          variant="ghost"
          size="icon"
          onClick={toggleSidebar}
          className="h-10 w-10"
          aria-label="Expand sidebar"
          title="Expand sidebar"
        >
          <PanelLeftOpen className="h-5 w-5" />
        </Button>
        <div className="h-px w-8 bg-sidebar-border my-1" aria-hidden="true" />
        <Button
          asChild
          variant="ghost"
          size="icon"
          className={`h-10 w-10 ${
            isDashboard
              ? "bg-sidebar-accent text-sidebar-accent-foreground"
              : ""
          }`}
          aria-label="Dashboard"
          aria-current={isDashboard ? "page" : undefined}
          title="Dashboard"
        >
          <Link to="/">
            <LayoutDashboard className="h-5 w-5" />
          </Link>
        </Button>
        <Button
          variant="ghost"
          size="icon"
          onClick={toggleSidebar}
          className="h-10 w-10 relative"
          aria-label={stackCount > 0 ? `Stacks (${stackCount})` : "Stacks"}
          title={
            stackCount > 0
              ? `Stacks (${stackCount}) — click to expand`
              : "Stacks — click to expand"
          }
        >
          <Boxes className="h-5 w-5" />
          {stackCount > 0 && (
            <Badge
              variant="secondary"
              className="absolute -top-0.5 -right-0.5 h-4 min-w-4 px-1 text-[9px] leading-none tabular-nums pointer-events-none"
            >
              {stackCount > 99 ? "99+" : stackCount}
            </Badge>
          )}
          {updateCount > 0 && (
            <span
              className="absolute -top-0.5 -left-0.5 h-2 w-2 rounded-full bg-amber-500 pointer-events-none"
              aria-hidden="true"
            />
          )}
        </Button>
        <div className="flex-1" aria-hidden="true" />
        <Button
          asChild
          variant="ghost"
          size="icon"
          className={`h-10 w-10 ${
            isSettings
              ? "bg-sidebar-accent text-sidebar-accent-foreground"
              : ""
          }`}
          aria-label="Settings"
          aria-current={isSettings ? "page" : undefined}
          title="Settings"
        >
          <Link to="/settings">
            <Settings className="h-5 w-5" />
          </Link>
        </Button>
      </aside>
    );
  }

  const hasFilters = searchQuery || statusFilter !== "all";

  const sidebarContent = (
    <>
      <div className="p-3 border-b space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-1.5">
            <Boxes className="h-4 w-4" />
            Stacks
            <span className="text-muted-foreground font-normal">
              ({stacks.length})
            </span>
          </h2>
          <div className="flex items-center gap-0.5">
            {updateCount > 0 && (
              <Link
                to="/?tab=updates"
                title={`${updateCount} update${updateCount > 1 ? "s" : ""} available`}
              >
                <Badge className="h-5 gap-1 bg-amber-500 text-white border-transparent hover:bg-amber-600 px-1.5 text-[10px]">
                  <ArrowUpCircle className="h-3 w-3" />
                  {updateCount}
                </Badge>
              </Link>
            )}
            <Button
              variant={selecting ? "secondary" : "ghost"}
              size="icon"
              className="h-6 w-6"
              onClick={() => (selecting ? exitSelectMode() : setSelecting(true))}
              aria-label={selecting ? "Exit selection" : "Select stacks"}
              aria-pressed={selecting}
              title={selecting ? "Exit selection" : "Select multiple stacks"}
            >
              <ListChecks className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="h-6 w-6"
              onClick={toggleSidebar}
              aria-label="Collapse sidebar"
              title="Collapse sidebar"
            >
              <PanelLeftClose className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>

        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search stacks..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full h-7 pl-7 pr-2 text-xs rounded-md border bg-background focus:outline-hidden focus:ring-1 focus:ring-ring"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery("")}
              className="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-sidebar-foreground"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </div>

        <div className="flex items-center gap-1 flex-wrap">
          {(["all", "running", "stopped", "error"] as const).map((key) => (
            <button
              key={key}
              onClick={() => setStatusFilter(key)}
              className={`inline-flex items-center gap-1 h-5 px-1.5 rounded text-[10px] font-medium transition-colors ${
                statusFilter === key
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground hover:bg-muted/80"
              }`}
            >
              {key === "all" && "All"}
              {key === "running" && (
                <>
                  <span className="h-1.5 w-1.5 rounded-full bg-current" />
                  Running
                </>
              )}
              {key === "stopped" && (
                <>
                  <span className="h-1.5 w-1.5 rounded-sm bg-current" />
                  Stopped
                </>
              )}
              {key === "error" && (
                <>
                  <span className="h-1.5 w-1.5 rounded-full bg-current" />
                  Error
                </>
              )}
            </button>
          ))}
          <div className="flex-1" />
          <button
            onClick={() => setSortBy(sortBy === "name" ? "status" : "name")}
            className="inline-flex items-center gap-0.5 h-5 px-1.5 rounded text-[10px] text-muted-foreground hover:bg-muted transition-colors"
            title={`Sort by ${sortBy === "name" ? "name" : "status"}. Click to toggle.`}
          >
            <ArrowUpDown className="h-2.5 w-2.5" />
            {sortBy === "name" ? "A-Z" : "St"}
          </button>
        </div>
      </div>

      {selecting && (
        <div className="px-3 py-2 border-b bg-muted/30 space-y-1.5">
          <div className="flex items-center justify-between text-[11px]">
            <span className="font-medium">
              {selectedIds.size} selected
            </span>
            <button
              onClick={selectAllVisible}
              className="text-primary hover:underline"
            >
              {selectedIds.size === filteredStacks.length && filteredStacks.length > 0
                ? "Clear all"
                : "Select all"}
            </button>
          </div>
          <div className="grid grid-cols-4 gap-1">
            <Button
              variant="outline"
              size="sm"
              className="h-7 px-0"
              disabled={selectedIds.size === 0 || bulkPending}
              onClick={() => runBulk("start")}
              title="Start selected"
            >
              <Play className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-7 px-0"
              disabled={selectedIds.size === 0 || bulkPending}
              onClick={() => runBulk("stop")}
              title="Stop selected"
            >
              <SquareIcon className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-7 px-0"
              disabled={selectedIds.size === 0 || bulkPending}
              onClick={() => runBulk("restart")}
              title="Restart selected"
            >
              <RotateCcw className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-7 px-0"
              disabled={selectedIds.size === 0 || bulkPending}
              onClick={() => runBulk("pull")}
              title="Pull selected"
            >
              <ArrowDownToLine className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      )}

      {hasFilters && (
        <div className="px-3 py-1 border-b text-[10px] text-muted-foreground">
          {filteredStacks.length} of {stacks.length} stacks
          <button
            onClick={() => {
              setSearchQuery("");
              setStatusFilter("all");
            }}
            className="ml-1.5 text-primary hover:underline"
          >
            Clear
          </button>
        </div>
      )}

      <ScrollArea className="flex-1">
        <div className="p-2 space-y-0.5">
          {!selecting && pinnedVisible.length > 0 && (
            <div className="mb-1">
              <div className="flex items-center gap-1 px-2 pt-1 pb-0.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                <Star className="h-2.5 w-2.5 fill-current text-warning" />
                Pinned
              </div>
              {pinnedVisible.map(renderStack)}
              <div className="h-px bg-sidebar-border my-1" />
            </div>
          )}
          {isLoading ? (
            <div className="px-2 py-4 text-sm text-muted-foreground">
              Loading...
            </div>
          ) : filteredStacks.length === 0 ? (
            <div className="px-2 py-4 text-sm text-muted-foreground">
              {hasFilters ? "No stacks match filters" : "No stacks found"}
            </div>
          ) : useGroups && !selecting ? (
            configuredDirs.length > 1 ? (
              treeByRoot.map((rootGroup) => {
                const isRootCollapsed = collapsedGroups.has(rootGroup.rootPath);
                const totalStacks = rootGroup.nodes.reduce(
                  (sum, n) => sum + countTreeNodeStacks(n),
                  0,
                );
                return (
                  <div key={rootGroup.rootPath}>
                    <button
                      onClick={() => toggleGroup(rootGroup.rootPath)}
                      className="flex items-center gap-1.5 w-full px-2 pt-2 pb-0.5 text-[10px] font-semibold text-muted-foreground uppercase tracking-wider hover:text-sidebar-foreground transition-colors"
                      title={rootGroup.rootPath}
                    >
                      {isRootCollapsed ? (
                        <ChevronRight className="h-3 w-3 shrink-0" />
                      ) : (
                        <ChevronDown className="h-3 w-3 shrink-0" />
                      )}
                      <FolderOpen className="h-3 w-3 shrink-0" />
                      <span className="truncate flex-1 text-left">
                        {rootGroup.rootName}
                      </span>
                      <span className="text-[9px] font-normal tabular-nums">
                        {totalStacks}
                      </span>
                    </button>
                    {!isRootCollapsed &&
                      rootGroup.nodes.map((node, i) => renderTreeNode(node, 0, i === 0))}
                  </div>
                );
              })
            ) : (
              tree.map((node, i) => renderTreeNode(node, 0, i === 0))
            )
          ) : (
            filteredStacks.map(renderStack)
          )}
        </div>
      </ScrollArea>

      {backupStatus?.schedulerRunning && (
        <div className="px-3 py-1.5 border-t flex items-center gap-1.5 text-[10px] text-muted-foreground">
          <Database className="h-3 w-3 shrink-0" />
          <span className="truncate">
            {backupStatus.lastRun
              ? `Backup ${relativeTime(backupStatus.lastRun.finishedAt || backupStatus.lastRun.startedAt)}`
              : "No backups yet"}
            {backupStatus.nextRunAt && ` · next in ${untilTime(backupStatus.nextRunAt)}`}
          </span>
        </div>
      )}
    </>
  );

  return (
    <>
      {sidebarOpen && (
        <div className="lg:hidden fixed inset-0 z-40">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={toggleSidebar}
            aria-hidden="true"
          />
          <aside className="relative flex flex-col w-72 h-full bg-sidebar text-sidebar-foreground shadow-xl">
            {sidebarContent}
          </aside>
        </div>
      )}

      <aside
        className="hidden lg:flex flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground transition-[none] relative"
        style={{ width: sidebarWidth }}
      >
        {sidebarContent}
        <div
          className="absolute right-0 top-0 bottom-0 w-1 cursor-col-resize hover:bg-sidebar-ring/20 active:bg-sidebar-ring/30 transition-colors z-10"
          onMouseDown={handleMouseDown}
          role="separator"
          aria-orientation="vertical"
          aria-label="Drag to resize sidebar"
          aria-valuenow={sidebarWidth}
          title="Drag to resize"
        />
      </aside>
    </>
  );
}
