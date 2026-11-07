import { useEffect, useState } from 'react';

export function useTheme() {
  const [darkMode, setDarkMode] = useState(() => document.documentElement.dataset.theme !== 'light');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    document.documentElement.dataset.theme = darkMode ? 'dark' : 'light';
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', darkMode ? '#151719' : '#f6f7f8');
  }, [darkMode]);

  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === 'sqnsl-theme' || event.key === null) {
        setDarkMode(event.newValue !== 'light');
      }
    };
    window.addEventListener('storage', sync);
    return () => window.removeEventListener('storage', sync);
  }, []);

  const toggleTheme = () => {
    const next = !darkMode;
    setDarkMode(next);
    setError(null);
    try {
      localStorage.setItem('sqnsl-theme', next ? 'dark' : 'light');
    } catch (err) {
      console.error('Unable to save theme preference', err);
      setError('Your browser could not save the theme preference. It still applies for this session.');
    }
  };

  return { darkMode, toggleTheme, error };
}
