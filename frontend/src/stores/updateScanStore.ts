import { create } from 'zustand'

interface UpdateScanState {
  isScanning: boolean
  scanStartedAt: number | null
  startScan: () => void
  finishScan: () => void
  resetScan: () => void
}

export const useUpdateScanStore = create<UpdateScanState>()((set) => ({
  isScanning: false,
  scanStartedAt: null,

  startScan: () => {
    set({ isScanning: true, scanStartedAt: Date.now() })
  },

  finishScan: () => {
    set({ isScanning: false })
  },

  resetScan: () => {
    set({ isScanning: false, scanStartedAt: null })
  },
}))
