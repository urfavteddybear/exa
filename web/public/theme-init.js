(function () {
  try {
    var isOps = /^\/(admin|login|setup)(\/|$)/.test(window.location.pathname);
    var key = isOps ? 'exa_theme_admin' : 'exa_theme_public';
    var fallback = isOps ? 'dark' : 'light';
    var saved = localStorage.getItem(key);
    var theme = saved === 'dark' || saved === 'light' ? saved : fallback;
    document.documentElement.setAttribute('data-theme', theme);
  } catch (e) {}
})();
