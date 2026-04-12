import { create } from 'zustand'

interface StackState {
  selectedStackId: string | null
  activeTab: string
  setSelectedStack: (id: string | null) => void
  setActiveTab: (tab: string) => void
}

export const useStackStore = create<StackState>()((set) => ({
  selectedStackId: null,
  activeTab: 'overview',

  setSelectedStack: (id) => set({ selectedStackId: id }),
  setActiveTab: (tab) => set({ activeTab: tab }),
}))
