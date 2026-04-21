import { useState, useMemo, useCallback, useRef, useEffect } from "react";
import { Link, useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useUIStore } from "@/stores/uiStore";
import { stacksApi } from "@/lib/api";
import {
  Search,
  X,
  Play,
  Square,
  AlertTriangle,
  HelpCircle,
  Minus,
  ChevronDown,
  ChevronRight,
  ArrowUpDown,
  Boxes,
} from "lucide-react";
import type { Stack, StackStatus } from "@/types";

const statusIcon: Record<
  StackStatus,
  { icon: React.ComponentType<{ className?: string }>; className: string }
> = {
  running: { icon: Play, className: "text-green-500" },
  partial: { icon: Minus, className: "text-yellow-500" },
  stopped: { icon: Square, className: "text-muted-foreground" },
  error: { icon: AlertTriangle, className: "text-red-500" },
  unknown: { icon: HelpCircle, className: "text-gray-400" },
};

function groupStacksByDirectory(stacks: Stack[]) {
  const groups = new Map<string, Stack[]>();
  for (const stack of stacks) {
    if (!groups.has(stack.directory)) groups.set(stack.directory, []);
    groups.get(stack.directory)!.push(stack);
  }
  const result: { dirName: string; dirPath: string; stacks: Stack[] }[] = [];
  for (const [dirPath, dirStacks] of groups) {
    const parts = dirPath.split("/");
    result.push({
      dirName: parts[parts.length - 1] || dirPath,
      dirPath,
      stacks: dirStacks,
    });
  }
  return result.sort((a, b) => a.dirName.localeCompare(b.dirName));
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
  const sidebarWidth = useUIStore((s) => s.sidebarWidth);
  const setSidebarWidth = useUIStore((s) => s.setSidebarWidth);
  const location = useLocation();
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

  const { data: stacks = [], isLoading } = useQuery({
    queryKey: ["stacks"],
    queryFn: () => stacksApi.list(),
    staleTime: 30_000,
  });

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

  const grouped = useMemo(
    () => groupStacksByDirectory(filteredStacks),
    [filteredStacks],
  );
  const useGroups = useMemo(() => {
    const allGrouped = groupStacksByDirectory(stacks);
    return allGrouped.some((g) => g.stacks.length > 1);
  }, [stacks]);

  const toggleGroup = useCallback((dirPath: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(dirPath)) next.delete(dirPath);
      else next.add(dirPath);
      return next;
    });
  }, []);

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

  const renderStack = (stack: Stack) => {
    const isActive = location.pathname.startsWith(`/stacks/${stack.id}`);
    const cfg = statusIcon[stack.status] || statusIcon.unknown;
    const Icon = cfg.icon;
    return (
      <Link
        key={stack.id}
        to={`/stacks/${stack.id}`}
        className={`flex items-center gap-2 px-3 py-1.5 rounded text-sm transition-colors ${
          isActive
            ? "bg-accent text-accent-foreground font-medium"
            : "hover:bg-accent/50 text-foreground"
        }`}
        aria-label={`${stack.projectName} - ${stack.status}`}
      >
        <Icon className={`h-3.5 w-3.5 flex-shrink-0 ${cfg.className}`} />
        <span className="flex-1 truncate">{stack.projectName}</span>
        {stack.containerCount != null && stack.containerCount > 0 && (
          <Badge
            variant="secondary"
            className="h-4 min-w-[1.25rem] px-1 text-[10px] leading-none"
          >
            {stack.containerCount}
          </Badge>
        )}
        {stack.isGitRepo && stack.gitDirty && (
          <span
            className="h-1.5 w-1.5 rounded-full bg-orange-400 flex-shrink-0"
            title="Uncommitted changes"
          />
        )}
      </Link>
    );
  };

  if (!sidebarOpen) {
    return (
      <aside className="hidden md:flex flex-col border-r bg-card">
        <Button
          variant="ghost"
          size="icon"
          onClick={toggleSidebar}
          className="fixed left-0 top-1/2 -translate-y-1/2 z-50 min-h-[44px] min-w-[44px]"
          aria-label="Open sidebar"
          title="Open sidebar"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <polyline points="9 18 15 12 9 6" />
          </svg>
        </Button>
      </aside>
    );
  }

  const hasFilters = searchQuery || statusFilter !== "all";

  return (
    <aside
      className="hidden lg:flex flex-col border-r bg-card transition-[none] relative"
      style={{ width: sidebarWidth }}
    >
      <div className="p-3 border-b space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-1.5">
            <Boxes className="h-4 w-4" />
            Stacks
            <span className="text-muted-foreground font-normal">
              ({stacks.length})
            </span>
          </h2>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={toggleSidebar}
            aria-label="Close sidebar"
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search stacks..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full h-7 pl-7 pr-2 text-xs rounded-md border bg-background focus:outline-none focus:ring-1 focus:ring-ring"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery("")}
              className="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
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
          {isLoading ? (
            <div className="px-2 py-4 text-sm text-muted-foreground">
              Loading...
            </div>
          ) : filteredStacks.length === 0 ? (
            <div className="px-2 py-4 text-sm text-muted-foreground">
              {hasFilters ? "No stacks match filters" : "No stacks found"}
            </div>
          ) : useGroups ? (
            grouped.map((group) => {
              const isCollapsed = collapsedGroups.has(group.dirPath);
              return (
                <div key={group.dirPath}>
                  <button
                    onClick={() => toggleGroup(group.dirPath)}
                    className="flex items-center gap-1.5 w-full px-2 pt-2 pb-0.5 text-[10px] font-medium text-muted-foreground uppercase tracking-wider hover:text-foreground transition-colors"
                    title={group.dirPath}
                  >
                    {isCollapsed ? (
                      <ChevronRight className="h-3 w-3 flex-shrink-0" />
                    ) : (
                      <ChevronDown className="h-3 w-3 flex-shrink-0" />
                    )}
                    <span className="truncate flex-1 text-left">
                      {group.dirName}
                    </span>
                    <span className="text-[9px] font-normal tabular-nums">
                      {group.stacks.length}
                    </span>
                  </button>
                  {!isCollapsed && group.stacks.map(renderStack)}
                </div>
              );
            })
          ) : (
            filteredStacks.map(renderStack)
          )}
        </div>
      </ScrollArea>

      <div
        className="absolute right-0 top-0 bottom-0 w-1 cursor-col-resize hover:bg-primary/20 active:bg-primary/30 transition-colors z-10"
        onMouseDown={handleMouseDown}
        role="separator"
        aria-orientation="vertical"
        aria-label="Drag to resize sidebar"
        aria-valuenow={sidebarWidth}
        title="Drag to resize"
      />
    </aside>
  );
}
