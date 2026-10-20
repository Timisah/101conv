// Runs before the app and styles load, including on login and setup pages.
(() => {
  const key = '101conv-theme-preference';
  const media = window.matchMedia('(prefers-color-scheme: dark)');
  const listeners = new Set();
  const valid = value => ['system', 'light', 'dark'].includes(value) ? value : 'system';
  const read = () => { try { return valid(localStorage.getItem(key)); } catch { return 'system'; } };
  // Deliberately ignore the old toggle's persisted value: only an explicit
  // three-way preference should override the system setting.
  let preference = read();
  const apply = () => {
    const dark = preference === 'dark' || (preference === 'system' && media.matches);
    const resolved = dark ? 'dark' : 'light';
    const color = dark ? '#192620' : '#ffffff';
    const root = document.documentElement;
    root.dataset.theme = resolved;
    root.style.colorScheme = resolved;
    root.style.backgroundColor = color;
    document.querySelector('meta[name="theme-color"]').content = color;
    document.querySelector('meta[name="color-scheme"]').content = resolved;
    document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]').content = dark ? 'black' : 'default';
    listeners.forEach(listener => listener());
  };
  window.conv101Theme = {
    getPreference: () => preference,
    setPreference: value => {
      preference = valid(value);
      try { localStorage.setItem(key, preference); } catch { /* Keep working in this tab when storage is unavailable. */ }
      apply();
    },
    subscribe: listener => { listeners.add(listener); return () => listeners.delete(listener); },
  };
  media.addEventListener('change', apply);
  window.addEventListener('storage', event => {
    if (event.key === key || event.key === null) { preference = read(); apply(); }
  });
  apply();
})();
