import {useSyncExternalStore} from 'react';
import {Moon, Sun} from 'lucide-react';
import {getTheme, setTheme, subscribeTheme} from './theme';

export function ThemeToggle() {
  const theme = useSyncExternalStore(subscribeTheme, getTheme, () => 'light');
  const dark = theme === 'dark';
  const label = dark ? '切换到亮色模式' : '切换到暗色模式';
  return <button type="button" className="icon-button theme-toggle" title={label} aria-label={label} aria-pressed={dark} onClick={() => setTheme(dark ? 'light' : 'dark')}>
    {dark ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />}
  </button>;
}
