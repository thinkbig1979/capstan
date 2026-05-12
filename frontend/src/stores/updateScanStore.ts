import { create } from 'zustand'

interface UpdateScanState {
  isScanning: boolean
  startScan: () => void
  finishScan: () => void
  resetScan: () => void
}

export const useUpdateScanStore = create<UpdateScanState>()((set) => ({
  isScanning: false,

  startScan: () => {
    set({ isScanning: true })
  },

  finishScan: () => {
    set({ isScanning: false })
  },

  resetScan: () => {
    set({ isScanning: false })
  },
}))
