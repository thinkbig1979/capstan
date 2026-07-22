import { useCallback, useRef } from 'react'

interface ResizeHandleProps {
  sidebarWidth: number
  onWidthChange: (width: number) => void
}

export function ResizeHandle({ sidebarWidth, onWidthChange }: ResizeHandleProps) {
  const isDragging = useRef(false)
  const dragWidthRef = useRef(0)

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault()
      isDragging.current = true
      dragWidthRef.current = sidebarWidth
      document.body.style.cursor = 'col-resize'
      document.body.style.userSelect = 'none'
      const startX = e.clientX
      const onMove = (ev: MouseEvent) => {
        if (!isDragging.current) return
        onWidthChange(sidebarWidth + ev.clientX - startX)
      }
      const onUp = () => {
        isDragging.current = false
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
        document.removeEventListener('mousemove', onMove)
        document.removeEventListener('mouseup', onUp)
      }
      document.addEventListener('mousemove', onMove)
      document.addEventListener('mouseup', onUp)
    },
    [sidebarWidth, onWidthChange],
  )

  return (
    <div
      className="absolute right-0 top-0 bottom-0 w-1 cursor-col-resize hover:bg-sidebar-ring/20 active:bg-sidebar-ring/30 transition-colors z-10"
      onMouseDown={handleMouseDown}
      role="separator"
      aria-orientation="vertical"
      aria-label="Drag to resize sidebar"
      aria-valuenow={sidebarWidth}
      title="Drag to resize"
    />
  )
}
