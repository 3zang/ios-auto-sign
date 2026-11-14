/* Theme preference shared by the console and the standalone install page.
   Loaded synchronously in <head> so the correct palette is applied before
   the first paint. Preference is stored locally; with no saved choice the
   system preference is used. */
(function () {
  'use strict';
  var KEY = 'ias-theme';

  function systemDark() {
    return !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
  }
  function read() {
    try {
      var saved = localStorage.getItem(KEY);
      if (saved === 'light' || saved === 'dark') return saved;
    } catch (e) { /* storage may be unavailable in private mode */ }
    return systemDark() ? 'dark' : 'light';
  }
  function apply(theme) {
    var t = theme === 'dark' ? 'dark' : 'light';
    document.documentElement.setAttribute('data-theme', t);
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute('content', t === 'dark' ? '#0b0b0d' : '#f5f5f7');
    // iOS status bar: dark theme -> white text ("black"), light -> dark text.
    var sb = document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]');
    if (sb) sb.setAttribute('content', t === 'dark' ? 'black' : 'default');
    var label = t === 'dark' ? '切换到浅色模式' : '切换到深色模式';
    var switches = document.querySelectorAll('.theme-switch');
    for (var i = 0; i < switches.length; i++) {
      switches[i].setAttribute('aria-checked', t === 'dark' ? 'true' : 'false');
      switches[i].setAttribute('aria-label', label);
      switches[i].title = label;
    }
  }
  function bind() {
    var switches = document.querySelectorAll('.theme-switch');
    for (var i = 0; i < switches.length; i++) {
      switches[i].addEventListener('click', function () {
        var next = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
        try { localStorage.setItem(KEY, next); } catch (e) { /* ignore */ }
        apply(next);
      });
    }
  }

  apply(read());
  window.IASTheme = { apply: apply };
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bind);
  } else {
    bind();
  }
})();
