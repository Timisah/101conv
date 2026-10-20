import { ChevronDown, Monitor, Moon, Sun } from 'lucide-react';
import { useThemePreference, type ThemePreference } from '../features/theme';

export function ThemePicker({ compact = false }: { compact?: boolean }) {
  const [preference, setPreference] = useThemePreference();
  const Icon = preference === 'system' ? Monitor : preference === 'dark' ? Moon : Sun;
  return <label className={compact ? 'theme-picker compact' : 'theme-picker'}>
    <Icon size={17} aria-hidden="true" />
    <select aria-label="Color theme" value={preference} onChange={event => setPreference(event.target.value as ThemePreference)}>
      <option value="system">System</option>
      <option value="light">Always light</option>
      <option value="dark">Always dark</option>
    </select>
    <ChevronDown size={14} aria-hidden="true" />
  </label>;
}

export function AppearanceSettings() {
  return <section className="admin-panel appearance-settings" id="appearance" aria-labelledby="appearance-title">
    <div className="panel-heading"><div><h2 id="appearance-title"><Monitor size={18} /> Appearance</h2><p>Follow your device’s appearance, or choose a fixed theme.</p></div></div>
    <div className="appearance-choice"><div><b>Color theme</b><p>System is the default and follows changes automatically.</p></div><ThemePicker /></div>
    <p className="field-hint">Saved in this browser. Applies to sign-in and guest pages too.</p>
  </section>;
}
