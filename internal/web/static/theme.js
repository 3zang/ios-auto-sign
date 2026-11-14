/* Theme preference: light / dark / auto (follow system).
   Loaded synchronously in <head> so the correct palette is applied before
   the first paint. Stored locally; default is "auto". */
(function () {
  'use strict';
  var KEY = 'ias-theme';
  var MODES = ['light', 'dark', 'auto'];

  function systemDark() {
    return !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
  }
  function stored() {
    try {
      var s = localStorage.getItem(KEY);
      if (s === 'light' || s === 'dark' || s === 'auto') return s;
    } catch (e) { /* storage may be unavailable in private mode */ }
    return null;
  }
  function mode() { return stored() || 'auto'; }
  function effective(m) { return m === 'auto' ? (systemDark() ? 'dark' : 'light') : m; }
  function lang() {
    try { return localStorage.getItem('ias-lang') === 'en' ? 'en' : 'zh'; } catch (e) { return 'zh'; }
  }
  function labels(lng) {
    if (lng === 'en') {
      return {
        light: { t: 'Light', full: 'Light' },
        dark: { t: 'Dark', full: 'Dark' },
        auto: { t: 'Auto', full: 'Auto (system)' },
        next: { light: 'Dark', dark: 'Auto', auto: 'Light' }
      };
    }
    return {
      light: { t: '白天', full: '白天' },
      dark: { t: '夜间', full: '夜间' },
      auto: { t: '自动', full: '自动（跟随系统）' },
      next: { light: '夜间', dark: '自动', auto: '白天' }
    };
  }

  var mql = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

  function apply(m) {
    var eff = effective(m);
    document.documentElement.setAttribute('data-theme', eff);
    document.documentElement.setAttribute('data-theme-mode', m);
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute('content', eff === 'dark' ? '#0b0b0d' : '#f5f5f7');
    // iOS status bar: dark theme -> white text ("black"), light -> dark text.
    var sb = document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]');
    if (sb) sb.setAttribute('content', eff === 'dark' ? 'black' : 'default');
    var L = labels(lang());
    var btns = document.querySelectorAll('.theme-toggle');
    for (var i = 0; i < btns.length; i++) {
      var b = btns[i];
      b.setAttribute('data-mode', m);
      b.setAttribute('aria-label', '主题：' + L[m].full + '，点击切换');
      b.title = '主题：' + L[m].full + '（点击切换到' + L.next[m] + '）';
      var lbl = b.querySelector('.theme-toggle-label');
      if (lbl) lbl.textContent = L[m].t;
    }
  }

  function bind() {
    var btns = document.querySelectorAll('.theme-toggle');
    for (var i = 0; i < btns.length; i++) {
      btns[i].addEventListener('click', function () {
        var cur = document.documentElement.getAttribute('data-theme-mode') || mode();
        var idx = MODES.indexOf(cur);
        if (idx < 0) idx = MODES.length - 1;
        var next = MODES[(idx + 1) % MODES.length];
        try { localStorage.setItem(KEY, next); } catch (e) { /* ignore */ }
        apply(next);
      });
    }
    // When in "auto", keep following the OS setting live.
    if (mql && mql.addEventListener) {
      mql.addEventListener('change', function () {
        if (mode() === 'auto') apply('auto');
      });
    }
    apply(mode());
  }

  apply(mode());
  window.IASTheme = { apply: apply };
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bind);
  } else {
    bind();
  }
})();
