import { createContext, useContext } from "react";

export interface ShellActions {
  openMenu: () => void;
  openPalette: () => void;
}

export const ShellContext = createContext<ShellActions>({ openMenu: () => undefined, openPalette: () => undefined });

export function useShell(): ShellActions {
  return useContext(ShellContext);
}
