import { useSyncExternalStore } from 'react';

export type ThemePreference = 'system' | 'light' | 'dark';

declare global {
  interface Window {
    conv101Theme: {
      getPreference: () => ThemePreference;
      setPreference: (value: ThemePreference) => void;
      subscribe: (listener: () => void) => () => void;
    };
  }
}

export function useThemePreference() {
  const theme = window.conv101Theme;
  const preference = useSyncExternalStore(theme.subscribe, theme.getPreference);
  return [preference, theme.setPreference] as const;
}
