import { useEffect } from "react";
import { useLocation } from "react-router";
import { useUIStore } from "@/stores/uiStore";
import { CollapsedRail } from "./sidebar/CollapsedRail";
import { SidebarBody } from "./sidebar/SidebarBody";
import { ResizeHandle } from "./sidebar/ResizeHandle";
import { useSidebarData } from "./sidebar/useSidebarData";
import { useSidebarFilters } from "./sidebar/useSidebarFilters";
import { useCollapsedGroups } from "./sidebar/useCollapsedGroups";
import { useSidebarSelection } from "./sidebar/useSidebarSelection";

export function Sidebar() {
  const sidebarOpen = useUIStore((s) => s.sidebarOpen);
  const toggleSidebar = useUIStore((s) => s.toggleSidebar);
  const closeSidebar = useUIStore((s) => s.closeSidebar);
  const sidebarWidth = useUIStore((s) => s.sidebarWidth);
  const setSidebarWidth = useUIStore((s) => s.setSidebarWidth);
  const pinnedStacks = useUIStore((s) => s.pinnedStacks);
  const togglePinnedStack = useUIStore((s) => s.togglePinnedStack);
  const location = useLocation();

  useEffect(() => {
    if (typeof window === "undefined") return;
    const mq = window.matchMedia("(max-width: 1023px)");
    if (mq.matches) {
      closeSidebar();
    }
  }, [location.pathname, closeSidebar]);

  const {
    searchQuery,
    setSearchQuery,
    statusFilter,
    setStatusFilter,
    sortBy,
    hasFilters,
    clearFilters,
  } = useSidebarFilters();

  const { collapsedGroups, toggleGroup } = useCollapsedGroups();

  const {
    stacks,
    isLoading,
    updateCount,
    backupStatus,
    configuredDirs,
    filteredStacks,
    pinnedVisible,
    tree,
    treeByRoot,
    useGroups,
  } = useSidebarData({ searchQuery, statusFilter, sortBy, pinnedStacks });

  const {
    selecting,
    setSelecting,
    selectedIds,
    bulkPending,
    toggleSelected,
    exitSelectMode,
    selectAllVisible,
    runBulk,
  } = useSidebarSelection(filteredStacks);

  if (!sidebarOpen) {
    return (
      <CollapsedRail
        stackCount={stacks.length}
        updateCount={updateCount}
        onToggleSidebar={toggleSidebar}
      />
    );
  }

  const bodyProps = {
    stacks,
    isLoading,
    updateCount,
    backupStatus,
    selecting,
    onToggleSelecting: () => (selecting ? exitSelectMode() : setSelecting(true)),
    onCollapseSidebar: toggleSidebar,
    searchQuery,
    onSearchChange: setSearchQuery,
    statusFilter,
    onStatusFilterChange: setStatusFilter,
    hasFilters,
    onClearFilters: clearFilters,
    selectedIds,
    bulkPending,
    onSelectAllVisible: selectAllVisible,
    onRunBulk: runBulk,
    onToggleSelect: toggleSelected,
    filteredStacks,
    pinnedVisible,
    pinnedStacks,
    onTogglePin: togglePinnedStack,
    useGroups,
    configuredDirs,
    tree,
    treeByRoot,
    collapsedGroups,
    toggleGroup,
  };

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
            <SidebarBody {...bodyProps} />
          </aside>
        </div>
      )}

      <aside
        className="hidden lg:flex flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground transition-[none] relative"
        style={{ width: sidebarWidth }}
      >
        <SidebarBody {...bodyProps} />
        <ResizeHandle sidebarWidth={sidebarWidth} onWidthChange={setSidebarWidth} />
      </aside>
    </>
  );
}
