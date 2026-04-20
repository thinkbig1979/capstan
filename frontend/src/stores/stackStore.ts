import { create } from 'zustand'

interface StackState {
  selectedStackId: string | null
  activeTab: string
  setSelectedStack: (id: string | null) => void
  setActiveTab: (tab: string) => void
  reset: () => void
}

const initialState = {
  selectedStackId: null as string | null,
  activeTab: 'overview',
}

export const useStackStore = create<StackState>()((set) => ({
  ...initialState,

  setSelectedStack: (id) => set({ selectedStackId: id }),
  setActiveTab: (tab) => set({ activeTab: tab }),
  reset: () => set(initialState),
}))
