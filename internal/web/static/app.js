(function () {
  'use strict';

  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function tr(s) { return window.IASI18N ? window.IASI18N.t(s) : s; }
  function fmtTime(t) {
    if (!t) return '-';
    try {
      var locale = window.IASI18N && window.IASI18N.lang === 'en' ? 'en-US' : 'zh-CN';
      return new Date(t).toLocaleString(locale);
    } catch (e) { return String(t); }
  }
  // Localized, fixed-format timestamp: yyyy-MM-dd HH:mm:ss in the viewer's
  // timezone. Used for update / signing times so the format is stable across
  // locales instead of toLocaleString's variable layout.
  function pad2(n) { return (n < 10 ? '0' : '') + n; }
  function fmtDateTime(t) {
    if (!t) return '-';
    try {
      var d = new Date(t);
      if (isNaN(d.getTime())) return String(t);
      return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) +
        ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds());
    } catch (e) { return String(t); }
  }
  function fmtSize(n) { return n ? (n / 1048576).toFixed(1) + ' MB' : '-'; }

  // Per-app version update detail: the latest version's changelog, truncated
  // inline. When the text is long a "更多" button appears; hovering it shows a
  // floating popup (appended to <body>, so it is never clipped by the card's
  // overflow:hidden and never expands the card) with the full content.
  function updateDetailHTML(latest) {
    if (!latest || !latest.localized_description) return '';
    var full = String(latest.localized_description);
    if (full.length <= 48) {
      return '<div class="app-update">' +
        '<span class="update-label">' + tr('更新详情') + '</span>' +
        '<span class="update-text">' + esc(full) + '</span>' +
      '</div>';
    }
    var trunc = full.slice(0, 48) + '…';
    return '<div class="app-update">' +
      '<span class="update-label">' + tr('更新详情') + '</span>' +
      '<span class="update-text">' + esc(trunc) + '</span>' +
      '<button class="more-btn" type="button" data-i18n-skip data-full="' + esc(full) + '" title="' + esc(full) + '">' + tr('更多') + '</button>' +
    '</div>';
  }

  async function api(path, opts) {
    var res = await fetch(path, opts || {});
    var ct = res.headers.get('content-type') || '';
    var data = ct.indexOf('json') >= 0 ? await res.json() : await res.text();
    if (!res.ok) throw new Error((data && data.error) || ('HTTP ' + res.status));
    return data;
  }
  function json(method, body) {
    return { method: method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}) };
  }
  var toastTimer;
  function toast(msg, bad) {
    var el = $('#toast');
    el.textContent = msg;
    el.className = 'toast' + (bad ? ' error' : '');
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.hidden = true; }, 4200);
  }
  function fail(e) { toast(e.message || String(e), true); }

  // Floating tooltip for the "更多" button: shows the full update detail in a
  // popup appended to <body>, so it is never clipped by the card's
  // overflow:hidden and never expands the card itself. The button also keeps a
  // native title attribute as a fallback when an older cached script is used.
  var updateTipEl = null, updateTipHideTimer = null;
  function hideUpdateTip() {
    clearTimeout(updateTipHideTimer);
    if (updateTipEl) { updateTipEl.remove(); updateTipEl = null; }
  }
  function scheduleHideTip() {
    clearTimeout(updateTipHideTimer);
    updateTipHideTimer = setTimeout(hideUpdateTip, 160);
  }
  function showUpdateTip(btn) {
    hideUpdateTip();
    var text = btn.getAttribute('data-full');
    if (!text) return;
    var tip = document.createElement('div');
    tip.className = 'update-pop';
    tip.setAttribute('data-i18n-skip', ''); // keep the changelog verbatim in EN mode
    tip.textContent = text;
    // Let the pointer move onto the popup so long text can be read / copied.
    tip.addEventListener('mouseenter', function () { clearTimeout(updateTipHideTimer); });
    tip.addEventListener('mouseleave', scheduleHideTip);
    document.body.appendChild(tip);
    var r = btn.getBoundingClientRect();
    var tw = tip.offsetWidth, th = tip.offsetHeight;
    var left = r.left + r.width / 2 - tw / 2;
    if (left < 8) left = 8;
    if (left + tw > window.innerWidth - 8) left = Math.max(8, window.innerWidth - tw - 8);
    var top = r.top - th - 8;
    if (top < 8) top = r.bottom + 8;
    tip.style.left = left + 'px';
    tip.style.top = top + 'px';
    updateTipEl = tip;
  }
  // Bind directly to each button (mouseenter/mouseleave do not bubble, so the
  // popup can never be dismissed by a stray mouseout from a delegated handler).
  function bindUpdateTips() {
    $$('#panel-apps .more-btn').forEach(function (btn) {
      btn.addEventListener('mouseenter', function () {
        btn.removeAttribute('title'); // custom popup takes over; avoid double tooltip
        showUpdateTip(btn);
      });
      btn.addEventListener('mouseleave', scheduleHideTip);
    });
  }
  window.addEventListener('scroll', hideUpdateTip, true);
  window.addEventListener('resize', hideUpdateTip);

  var me = null, pub = {};
  var loaders = {};
  var appFilter = 'all';
  var appSort = 'time';
  var jobArtifacts = [];
  var currentJobs = [];
  var artPage = 1;
  var artPageSize = 10;

  function applySiteTitle(title) {
    var t = String(title || '').trim() || 'iOS 自动签名';
    // tr() translates the default brand name but leaves a custom (user-owned)
    // title untouched, since it is data rather than UI copy.
    var shown = tr(t);
    var el = $('#site-title');
    if (el) el.textContent = shown;
    var authTitle = $('#auth-view h1');
    if (authTitle) authTitle.textContent = shown;
    document.title = tr(t + ' · 控制台');
  }

  // One form, two fields: a segmented control switches between login and
  // registration, so the two functions are never shown at the same time.
  function bindAuth() {
    var mode = 'login';
    var form = $('#auth-form');
    var segs = $$('.auth-seg button');
    function setMode(next) {
      mode = next;
      segs.forEach(function (b) { b.classList.toggle('active', b.dataset.mode === mode); });
      $('#auth-submit').textContent = mode === 'login' ? '登录' : '注册';
      $('#auth-hint').textContent = mode === 'login'
        ? '请输入账号密码以继续。'
        : '首个注册的账号将成为管理员，密码至少 8 位。';
      form.password.setAttribute('autocomplete', mode === 'login' ? 'current-password' : 'new-password');
    }
    segs.forEach(function (b) { b.onclick = function () { setMode(b.dataset.mode); }; });
    setMode('login');
    form.onsubmit = async function (e) {
      e.preventDefault();
      var btn = $('#auth-submit');
      btn.disabled = true;
      try {
        await api('/api/auth/' + mode, json('POST', {
          username: form.username.value, password: form.password.value
        }));
        location.reload();
      } catch (err) {
        fail(err);
        btn.disabled = false;
      }
    };
  }

  async function switchTab(name) {
    $$('.tab').forEach(function (b) { b.classList.toggle('active', b.dataset.tab === name); });
    $$('.panel').forEach(function (p) { p.hidden = p.id !== 'panel-' + name; });
    try { await loaders[name](); } catch (e) { fail(e); }
  }

  // ---------- overview ----------
  loaders.overview = async function () {
    var r = await Promise.all([
      api('/api/sources'), api('/api/apps'), api('/api/jobs'), api('/api/artifacts')
    ]);
    var sources = r[0], apps = r[1], jobs = r[2], arts = r[3];
    var ok = jobs.filter(function (j) { return j.status === 'succeeded'; }).length;
    var bad = jobs.filter(function (j) { return j.status === 'failed'; }).length;
    var enabledSources = sources.filter(function (s) { return s.enabled; }).length;
    var configuredApps = apps.filter(function (x) { return !!x.config; }).length;
    var features = ['订阅源自动轮询', '自定义名称与图标', '证书加密存储', 'Bark 更新提醒', '二维码扫码安装', '版本自动保留'];
    $('#panel-overview').innerHTML =
      '<section class="hero">' +
        '<div class="hero-copy">' +
          '<p class="eyebrow"><span class="eyebrow-dot"></span> AUTO SIGNING · OTA DISTRIBUTION</p>' +
          '<h1>让每一次更新，<br><span>自动签好名。</span></h1>' +
          '<p class="hero-lead">订阅软件源一有新版本，平台就按你设定的名称、图标与证书自动重签名，发布安装包并推送提醒。从同步到安装，全程无需手动操作。</p>' +
          '<div class="hero-actions">' +
            '<button class="button button-primary" type="button" data-hero-tab="apps">配置应用</button>' +
            '<button class="button button-quiet" type="button" data-hero-tab="install">查看可安装应用</button>' +
          '</div>' +
          '<div class="feature-list">' + features.map(function (f) {
            return '<span>' + esc(f) + '</span>';
          }).join('') + '</div>' +
        '</div>' +
        '<div class="hero-panel" aria-label="服务概览">' +
          '<div class="panel-topline"><span class="panel-dot"></span><span>服务概览</span><b class="panel-live">运行中</b></div>' +
          '<div class="hero-stats">' +
            '<div><span>订阅源（启用 / 全部）</span><b>' + enabledSources + ' / ' + sources.length + '</b></div>' +
            '<div><span>应用（已配置 / 全部）</span><b>' + configuredApps + ' / ' + apps.length + '</b></div>' +
            '<div><span>签名成功 / 失败</span><b>' + ok + ' / ' + bad + '</b></div>' +
            '<div><span>已发布产物</span><b>' + arts.length + '</b></div>' +
          '</div>' +
          '<div class="server-card">' +
            '<span class="server-icon" aria-hidden="true">↓</span>' +
            '<div><span class="server-label">安装地址</span>' +
              '<span class="server-value mono">' + esc(pub.public_base_url) + '/install</span></div>' +
          '</div>' +
          '<p class="panel-note">plist 生成模式 <b>' + esc(pub.plist_mode) + '</b> · 每应用保留最近 ' +
            pub.keep_versions + ' 个版本 · 每 ' + pub.poll_minutes + ' 分钟轮询一次。</p>' +
        '</div>' +
      '</section>' +
      '<div class="stack">' +
        '<div class="hint">签名程序：<span class="mono">' + esc(pub.zsign_path) +
          '</span> &nbsp;·&nbsp; 默认源：<span class="mono">' + esc(pub.default_source) + '</span></div>' +
        '<div class="muted">流程：订阅源刷新 → 勾选应用并保存配置 → 上传 p12 / 密码 / mobileprovision → 自动签名 → 安装页出现可安装版本。检测到新版本时会按应用推送 Bark 通知，签名任务记录仅保留最近 5 条。</div>' +
      '</div>';
    $$('#panel-overview button[data-hero-tab]').forEach(function (b) {
      b.onclick = function () { switchTab(b.dataset.heroTab); };
    });
  };

  // ---------- sources ----------
  loaders.sources = async function () {
    renderSources(await api('/api/sources'));
  };

  function renderSources(list) {
    var rows = list.map(function (s) {
      return '<tr><td data-i18n-skip><b>' + esc(s.name) + '</b></td>' +
        '<td class="mono source-url" title="' + esc(s.url) + '">' + esc(s.url) + '</td>' +
        '<td class="status-cell">' + (s.enabled ? '<span class="tag ok">启用</span>' : '<span class="tag">停用</span>') + '</td>' +
        '<td>' + s.interval_minutes + ' 分钟</td>' +
        '<td>' + esc(fmtTime(s.last_checked_at)) + '</td>' +
        '<td>' + (s.last_error ? '<span class="tag err">' + esc(s.last_error) + '</span>' : '-') + '</td>' +
        '<td class="source-actions"><div class="row-actions">' +
          '<button data-action="refresh-source" data-id="' + esc(s.id) + '">刷新</button>' +
          '<button data-action="toggle-source" data-id="' + esc(s.id) + '">' + (s.enabled ? '停用' : '启用') + '</button>' +
          '<button class="danger" data-action="delete-source" data-id="' + esc(s.id) + '">删除</button>' +
        '</div></td></tr>';
    }).join('');
    $('#panel-sources').innerHTML =
      '<div class="panel-head"><div>' +
        '<p class="section-kicker">SOURCES</p>' +
        '<h2>订阅软件源</h2></div>' +
        '<button class="button button-primary" type="button" data-action="run-sync">立即执行</button>' +
      '</div>' +
      '<div class="hint" style="margin-bottom:14px">点击「立即执行」会立即检查所有启用的软件源；发现新版本时自动创建签名任务，并推送 Bark 更新通知。</div>' +
      '<form id="source-form" class="form-row">' +
        '<label>名称<input name="name" placeholder="我的软件源"></label>' +
        '<label class="wide">JSON 地址<input name="url" placeholder="' + esc(pub.default_source) + '" required></label>' +
        '<label>轮询（分钟）<input name="interval" type="number" min="1" value="' + pub.poll_minutes + '"></label>' +
        '<label class="inline"><input name="enabled" type="checkbox" checked> 启用</label>' +
        '<button type="submit" class="primary">添加并同步</button>' +
      '</form>' +
      '<table><thead><tr><th>名称</th><th>地址</th><th>状态</th><th>间隔</th><th>上次检查</th><th>错误</th><th>操作</th></tr></thead>' +
      '<tbody>' + (rows || '<tr><td colspan="7" class="muted">暂无订阅源</td></tr>') + '</tbody></table>';

    $('#source-form').onsubmit = async function (e) {
      e.preventDefault();
      var f = e.target;
      try {
        await api('/api/sources', json('POST', {
          name: f.name.value || '软件源', url: f.url.value,
          enabled: f.enabled.checked, interval_minutes: Number(f.interval.value) || pub.poll_minutes
        }));
        toast('已添加，后台正在同步元数据');
        await loaders.sources();
      } catch (err) { fail(err); }
    };
  }

  // ---------- apps ----------
  loaders.apps = async function () {
    var r = await Promise.all([api('/api/apps'), api('/api/icons'), api('/api/certificates')]);
    renderApps(r[0], r[1], r[2]);
  };

  function renderApps(apps, icons, certs) {
    var iconMap = {};
    icons.forEach(function (i) { iconMap[i.id] = i; });
    var iconOpts = icons.filter(function (i) { return i.enabled; }).map(function (i) {
      return '<option value="' + esc(i.id) + '">' + esc(i.label || i.url) + '</option>';
    }).join('');
    var certOpts = certs.map(function (c) {
      return '<option value="' + esc(c.id) + '">' + esc(c.name) + (c.has_provision ? '' : '（缺描述文件）') + '</option>';
    }).join('');

    var configuredCount = apps.filter(function (row) { return !!row.config; }).length;
    var visible = (appFilter === 'configured'
      ? apps.filter(function (row) { return !!row.config; })
      : apps).slice();
    function appTime(row) {
      var latest = '';
      (row.versions || []).forEach(function (v) {
        var d = String(v.date || '');
        if (d > latest) latest = d;
      });
      return latest || String(row.app.updated_at || '');
    }
    visible.sort(function (x, y) {
      if (appSort === 'name') {
        return String(x.app.name || '').localeCompare(String(y.app.name || ''), 'zh-CN');
      }
      return appTime(y).localeCompare(appTime(x));
    });

    var cards = visible.map(function (row) {
      var a = row.app, c = row.config || {};
      var opts = c.sign_options || {};
      var versions = (row.versions || []).slice().sort(function (x, y) {
        return String(y.date).localeCompare(String(x.date));
      });
      var latest = versions.length ? versions[0] : null;
      var icon = a.icon_url ? '<img src="' + esc(a.icon_url) + '" alt="">' : '<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="">';
      var src = c.icon_source || 'original';
      return '<div class="card">' +
        '<div class="card-head">' + icon + '<div><div class="name" data-i18n-skip>' + esc(a.name) + '</div>' +
          '<div class="sub mono">' + esc(a.bundle_id) + '</div>' +
          '<div class="sub">' + (latest ? '版本 ' + esc(latest.version) : '暂无版本') + '</div>' +
          (latest ? '<div class="sub">' + tr('更新时间') + '：' + esc(fmtDateTime(latest.detected_at || latest.date)) + '</div>' : '') +
          updateDetailHTML(latest) + '</div>' +
          (row.config ? '<button type="button" class="danger card-delete" data-action="delete-app-config" data-id="' + esc(a.id) + '">' + tr('删除配置') + '</button>' : '') +
        '</div>' +
        '<div class="card-body">' +
          '<details><summary>配置签名元数据</summary>' +
          '<form class="app-config stack" data-id="' + esc(a.id) + '" style="margin-top:12px">' +
            '<label class="inline"><input type="checkbox" name="enabled" ' + (c.enabled ? 'checked' : '') + '> 启用自动签名</label>' +
            '<label>显示名称<input name="display_name" value="' + esc(c.display_name || '') + '" placeholder="' + esc(a.name) + '"></label>' +
            '<label>图标来源<select name="icon_source">' +
              '<option value="original"' + (src === 'original' ? ' selected' : '') + '>订阅源原始图标</option>' +
              '<option value="library"' + (src === 'library' ? ' selected' : '') + '>管理员图标库</option>' +
              '<option value="custom"' + (src === 'custom' ? ' selected' : '') + '>自定义 URL</option>' +
            '</select></label>' +
            '<label class="icon-lib"><span class="icon-lib-label">' + tr('选择图标') + '</span>' +
              '<select name="icon_library_id"><option value="">未选择</option>' + iconOpts + '</select>' +
              '<span class="icon-lib-current"><img class="icon-lib-preview" alt="' + esc(tr('图标预览')) + '" hidden>' +
              '<span class="icon-lib-name"></span></span></label>' +
            '<label class="icon-custom">自定义图标 URL<input name="icon_custom_url" value="' + esc(c.icon_custom_url || '') + '" placeholder="https://.../icon.png"></label>' +
            '<label>证书（p12 + 描述文件）<select name="certificate_id"><option value="">未选择</option>' + certOpts + '</select></label>' +
            '<label>包名覆盖<input name="bundle_id_override" value="' + esc(c.bundle_id_override || '') + '" placeholder="留空则使用 ' + esc(a.bundle_id) + '"></label>' +
            '<label>压缩级别（0-9，留空继承全局）<input name="zip_level" type="number" min="0" max="9" value="' + (opts.zip_level == null ? '' : esc(opts.zip_level)) + '"></label>' +
            '<label class="inline"><input type="checkbox" name="force" ' + (opts.force ? 'checked' : '') + '> 强制签名（-f）</label>' +
            '<label class="inline"><input type="checkbox" name="adhoc" ' + (opts.adhoc ? 'checked' : '') + '> ad-hoc 模式（不使用证书）</label>' +
            '<div class="inline"><button type="submit" class="primary">保存配置</button>' +
              '<button type="button" data-action="sign-app" data-id="' + esc(a.id) + '">立即签名</button></div>' +
          '</form></details>' +
        '</div></div>';
    }).join('');

    $('#panel-apps').innerHTML =
      '<div class="panel-head"><div>' +
        '<p class="section-kicker">APPS & SIGNING</p>' +
        '<h2>应用与签名配置</h2></div></div>' +
      '<div class="app-toolbar">' +
        '<div class="filter-seg" role="tablist" aria-label="应用筛选">' +
          '<button type="button" data-app-filter="configured" class="' + (appFilter === 'configured' ? 'active' : '') + '">已配置 ' + configuredCount + '</button>' +
          '<button type="button" data-app-filter="all" class="' + (appFilter === 'all' ? 'active' : '') + '">全部 ' + apps.length + '</button>' +
        '</div>' +
        '<div class="sort-seg" role="group" aria-label="排序方式">' +
          '<span class="sort-label">排序</span>' +
          '<button type="button" data-app-sort="time" class="' + (appSort === 'time' ? 'active' : '') + '">按时间</button>' +
          '<button type="button" data-app-sort="name" class="' + (appSort === 'name' ? 'active' : '') + '">按名称</button>' +
        '</div>' +
      '</div>' +
      '<div class="hint" style="margin-bottom:14px">自定义图标会写入 IPA（zsign -I），因此安装到设备后的桌面图标也会改变。勾选启用并选择证书后，检测到新版本时会自动创建签名任务。</div>' +
      '<div class="card-grid">' + (cards ||
        (appFilter === 'configured'
          ? '<div class="muted">还没有已配置的应用，切换到「全部」勾选并保存配置。</div>'
          : '<div class="muted">暂无应用，请先在「订阅源」中刷新元数据。</div>')) + '</div>';

    bindUpdateTips();
    $$('.app-config').forEach(function (f) {
      var c = f.dataset.id;
      // restore current selections that were rendered as plain options
      var row = apps.find(function (x) { return x.app.id === c; });
      var cfg = (row && row.config) || {};
      if (cfg.icon_library_id) f.icon_library_id.value = cfg.icon_library_id;
      if (cfg.certificate_id) f.certificate_id.value = cfg.certificate_id;
      var iconPreview = f.querySelector('.icon-lib-preview');
      var iconName = f.querySelector('.icon-lib-name');
      function updateIconPreview() {
        var item = iconMap[f.icon_library_id.value];
        if (!item) {
          iconPreview.hidden = true;
          iconPreview.removeAttribute('src');
          iconName.textContent = '';
          return;
        }
        iconPreview.hidden = false;
        iconPreview.src = item.url;
        iconName.textContent = item.label || item.url;
      }
      iconPreview.onerror = function () { iconPreview.hidden = true; };
      f.icon_library_id.onchange = updateIconPreview;
      updateIconPreview();
      function toggle() {
        f.querySelector('.icon-lib').hidden = f.icon_source.value !== 'library';
        f.querySelector('.icon-custom').hidden = f.icon_source.value !== 'custom';
      }
      f.icon_source.onchange = toggle;
      toggle();
      f.onsubmit = async function (e) {
        e.preventDefault();
        var z = f.zip_level.value;
        try {
          await api('/api/apps/' + encodeURIComponent(c) + '/config', json('PUT', {
            enabled: f.enabled.checked,
            display_name: f.display_name.value,
            icon_source: f.icon_source.value,
            icon_library_id: f.icon_library_id.value,
            icon_custom_url: f.icon_custom_url.value,
            bundle_id_override: f.bundle_id_override.value,
            certificate_id: f.certificate_id.value,
            sign_options: {
              zip_level: z === '' ? null : Number(z),
              force: f.force.checked,
              adhoc: f.adhoc.checked
            }
          }));
          toast('配置已保存');
        } catch (err) { fail(err); }
      };
    });
    $$('#panel-apps button[data-app-filter]').forEach(function (b) {
      b.onclick = function () {
        appFilter = b.dataset.appFilter;
        loaders.apps();
      };
    });
    $$('#panel-apps button[data-app-sort]').forEach(function (b) {
      b.onclick = function () {
        appSort = b.dataset.appSort;
        loaders.apps();
      };
    });
  }

  // ---------- icon library ----------
  loaders.icons = async function () {
    var list = await api('/api/icons');
    var admin = me.role === 'admin';
    var rows = list.map(function (i) {
      var thumb = i.url
        ? '<button class="icon-thumb-btn" type="button" data-icon-preview="' + esc(i.id) + '" title="预览图标">' +
            '<img class="icon-thumb" src="' + esc(i.url) + '" alt="预览 ' + esc(i.label || '') + '"></button>'
        : '<span class="muted">-</span>';
      return '<tr><td>' + thumb + '</td><td data-i18n-skip>' + esc(i.label || '-') + '</td>' +
        '<td class="mono">' + esc(i.url) + '</td>' +
        '<td class="status-cell">' + (i.enabled ? '<span class="tag ok">可用</span>' : '<span class="tag">停用</span>') + '</td>' +
        '<td>' + (admin ? '<button class="danger" data-action="delete-icon" data-id="' + esc(i.id) + '">删除</button>' : '') + '</td></tr>';
    }).join('');
    $('#panel-icons').innerHTML = '<p class="section-kicker">ICON LIBRARY</p><h2>图标库</h2>' +
      (admin ? '<form id="icon-form" class="form-row">' +
        '<label>名称<input name="label" placeholder="备用图标 A"></label>' +
        '<label class="wide">图标 URL（建议 HTTPS PNG）<input name="url" required></label>' +
        '<label>实时预览<img id="icon-form-preview" class="form-preview" alt="图标预览" hidden></label>' +
        '<label class="inline"><input type="checkbox" name="enabled" checked> 可用</label>' +
        '<button type="submit" class="primary">添加</button></form>'
        : '<div class="hint" style="margin-bottom:14px">图标库由管理员维护，你可在「应用与配置」中选用。点击缩略图可放大预览。</div>') +
      '<table><thead><tr><th>预览</th><th>名称</th><th>URL</th><th>状态</th><th>操作</th></tr></thead><tbody>' +
      (rows || '<tr><td colspan="5" class="muted">暂无图标</td></tr>') + '</tbody></table>';
    $$('#panel-icons button[data-icon-preview]').forEach(function (b) {
      b.onclick = function () {
        var item = list.find(function (x) { return x.id === b.dataset.iconPreview; });
        if (item) openIconPreview(item);
      };
    });
    if (admin) {
      var preview = $('#icon-form-preview');
      var urlInput = $('#icon-form input[name="url"]');
      preview.onerror = function () { preview.hidden = true; };
      urlInput.addEventListener('input', function () {
        var v = urlInput.value.trim();
        if (!v) { preview.hidden = true; preview.removeAttribute('src'); return; }
        preview.hidden = false;
        preview.src = v;
      });
      $('#icon-form').onsubmit = async function (e) {
        e.preventDefault();
        var f = e.target;
        try {
          await api('/api/icons', json('POST', { label: f.label.value, url: f.url.value, enabled: f.enabled.checked }));
          toast('图标已添加');
          await loaders.icons();
        } catch (err) { fail(err); }
      };
    }
  };

  // ---------- certificates ----------
  loaders.certs = async function () {
    var list = await api('/api/certificates');
    var rows = list.map(function (c) {
      return '<tr><td data-i18n-skip>' + esc(c.name) + '</td>' +
        '<td>' + (c.has_provision ? '<span class="tag ok">三件套完整</span>' : '<span class="tag err">缺描述文件</span>') + '</td>' +
        '<td>' + esc(fmtTime(c.created_at)) + '</td>' +
        '<td><button class="danger" data-action="delete-cert" data-id="' + esc(c.id) + '">删除</button></td></tr>';
    }).join('');
    $('#panel-certs').innerHTML = '<p class="section-kicker">CERTIFICATES</p><h2>签名证书</h2>' +
      '<div class="hint" style="margin-bottom:14px">正常重签名需要三件套：.p12 证书、证书密码、.mobileprovision 描述文件。三者均使用 AES-256-GCM 加密存储，接口不会返回明文。</div>' +
      '<form id="cert-form" class="form-row">' +
        '<label>名称<input name="name" placeholder="个人开发者证书"></label>' +
        '<label>证书密码<input name="password" type="password"></label>' +
        '<label>.p12 文件<input name="p12" type="file" accept=".p12,.pfx" required></label>' +
        '<label>.mobileprovision<input name="provision" type="file" accept=".mobileprovision,.prov" required></label>' +
        '<button type="submit" class="primary">上传并加密保存</button>' +
      '</form>' +
      '<table><thead><tr><th>名称</th><th>状态</th><th>上传时间</th><th>操作</th></tr></thead><tbody>' +
      (rows || '<tr><td colspan="4" class="muted">暂无证书</td></tr>') + '</tbody></table>';
    $('#cert-form').onsubmit = async function (e) {
      e.preventDefault();
      var f = e.target;
      var fd = new FormData();
      fd.append('name', f.name.value || '我的证书');
      fd.append('password', f.password.value);
      fd.append('p12', f.p12.files[0]);
      fd.append('provision', f.provision.files[0]);
      try {
        await api('/api/certificates', { method: 'POST', body: fd });
        toast('证书已加密保存');
        await loaders.certs();
      } catch (err) { fail(err); }
    };
  };

  // ---------- jobs & artifacts ----------
  loaders.jobs = async function () {
    var r = await Promise.all([api('/api/jobs'), api('/api/artifacts')]);
    currentJobs = r[0].slice().sort(function (a, b) { return String(b.created_at).localeCompare(String(a.created_at)); }).slice(0, 5);
    jobArtifacts = r[1].slice().sort(function (a, b) { return String(b.created_at).localeCompare(String(a.created_at)); });
    renderJobs();
  };

  function renderJobs() {
    function tag(st) {
      var cls = st === 'succeeded' ? 'ok' : (st === 'failed' ? 'err' : '');
      return '<span class="tag ' + cls + '">' + esc(st) + '</span>';
    }
    var jrows = currentJobs.map(function (j) {
      return '<tr><td class="mono">' + esc(j.id) + '</td><td>' + tag(j.status) + '</td>' +
        '<td>' + esc(fmtTime(j.created_at)) + '</td><td>' + esc(fmtTime(j.finished_at)) + '</td>' +
        '<td class="mono">' + esc(j.error || '-') + '</td></tr>';
    }).join('');

    var total = jobArtifacts.length;
    var pages = Math.max(1, Math.ceil(total / artPageSize));
    if (artPage > pages) artPage = pages;
    if (artPage < 1) artPage = 1;
    var start = (artPage - 1) * artPageSize;
    var pageArts = jobArtifacts.slice(start, start + artPageSize);
    var arows = pageArts.map(function (a) {
      return '<tr><td data-i18n-skip>' + esc(a.display_name || a.bundle_id) + '</td><td>' + esc(a.version) + '</td>' +
        '<td>' + esc(fmtDateTime(a.created_at)) + '</td><td>' + esc(fmtSize(a.size)) + '</td>' +
        '<td><a class="link" href="/ipa/' + esc(a.id) + '.ipa">下载</a></td>' +
        '<td><button class="danger" data-action="delete-artifact" data-id="' + esc(a.id) + '">' + tr('删除') + '</button></td></tr>';
    }).join('');

    $('#panel-jobs').innerHTML = '<p class="section-kicker">JOBS & ARTIFACTS</p><h2>任务与产物</h2>' +
      '<h2 style="margin-bottom:14px">' + tr('已发布产物') + '（' + tr('每应用保留') + ' ' + pub.keep_versions + ' ' + tr('个版本') + '）</h2>' +
      '<table><thead><tr><th>' + tr('应用') + '</th><th>' + tr('版本') + '</th><th>' + tr('签名时间') + '</th><th>' + tr('大小') + '</th><th>' + tr('下载') + '</th><th>' + tr('操作') + '</th></tr></thead><tbody>' +
      (arows || '<tr><td colspan="6" class="muted">' + tr('暂无产物') + '</td></tr>') + '</tbody></table>' +
      (total > 0 ? '<div class="pager">' +
        '<button data-page="prev"' + (artPage <= 1 ? ' disabled' : '') + '>' + tr('上一页') + '</button>' +
        '<span class="pager-info">' + tr('第') + ' ' + artPage + ' / ' + pages + ' ' + tr('页') + ' · ' + tr('共') + ' ' + total + ' ' + tr('条') + '</span>' +
        '<button data-page="next"' + (artPage >= pages ? ' disabled' : '') + '>' + tr('下一页') + '</button>' +
        '<label class="pager-size">' + tr('每页') + '<select data-page-size>' +
          [5, 10, 20, 50].map(function (n) { return '<option value="' + n + '"' + (n === artPageSize ? ' selected' : '') + '>' + n + '</option>'; }).join('') +
        '</select>' + tr('条') + '</label>' +
      '</div>' : '') +
      '<h2 style="margin-top:26px">' + tr('签名任务') + '（' + tr('仅保留最近 5 条') + '）</h2>' +
      '<table><thead><tr><th>' + tr('任务 ID') + '</th><th>' + tr('状态') + '</th><th>' + tr('创建') + '</th><th>' + tr('完成') + '</th><th>' + tr('错误') + '</th></tr></thead><tbody>' +
      (jrows || '<tr><td colspan="5" class="muted">' + tr('暂无任务') + '</td></tr>') + '</tbody></table>';

    $$('#panel-jobs button[data-page]').forEach(function (b) {
      b.onclick = function () {
        if (b.disabled) return;
        if (b.dataset.page === 'prev' && artPage > 1) artPage--;
        else if (b.dataset.page === 'next' && artPage < pages) artPage++;
        renderJobs();
      };
    });
    var sizeSel = $('#panel-jobs select[data-page-size]');
    if (sizeSel) {
      sizeSel.onchange = function () {
        artPageSize = Number(sizeSel.value) || 10;
        artPage = 1;
        renderJobs();
      };
    }
  }

  // ---------- settings centre ----------
  loaders.settings = async function () {
    renderSettings(await api('/api/settings'));
  };

  function renderSettings(st) {
    if (!st.editable) {
      $('#panel-settings').innerHTML =
        '<p class="section-kicker">SETTINGS</p><h2>设置中心</h2>' +
        '<div class="hint">当前站点标题：<b>' + esc(st.site_title) + '</b>。全局设置（标题、Bark 通知、版本保留）仅管理员可修改。</div>' +
        '<div class="hint" style="margin-top:14px">当前每应用保留最近 <b>' + (pub.keep_versions || 2) + '</b> 个已签名版本。主题可在右上角的开关随时切换，偏好保存在当前浏览器。</div>';
      return;
    }
    $('#panel-settings').innerHTML =
      '<p class="section-kicker">SETTINGS</p><h2>设置中心</h2>' +
      '<form id="settings-form" class="stack settings-card">' +
        '<div><h3>顶部标题</h3><p class="settings-note">自定义控制台左上角显示的名称，保存后立即生效。</p></div>' +
        '<label>站点标题<input name="site_title" maxlength="60" value="' + esc(st.site_title) + '" placeholder="iOS 自动签名"></label>' +
        '<div class="settings-divider"></div>' +
        '<div><h3>' + tr('版本保留') + '</h3><p class="settings-note">' + tr('每个应用最多保留的已签名版本数量；超过后最旧的版本会被自动清理，对应安装入口一并移除。') + '</p></div>' +
        '<label>' + tr('每应用保留版本数') + '<input name="keep_versions" type="number" min="1" max="50" value="' + (st.keep_versions || pub.keep_versions || 2) + '"></label>' +
        '<div class="settings-divider"></div>' +
        '<div><h3>Bark 更新通知</h3><p class="settings-note">订阅源检测到新版本时，按应用推送一条「软件有更新」通知。首次添加软件源不会推送历史版本。</p></div>' +
        '<label class="inline"><input type="checkbox" name="bark_enabled" ' + (st.bark_enabled ? 'checked' : '') + '> 启用 Bark 推送</label>' +
        '<label>服务器地址<input name="bark_server" value="' + esc(st.bark_server) + '" placeholder="https://api.day.app"></label>' +
        '<label>设备 Key<input name="bark_key" value="' + esc(st.bark_key) + '" placeholder="Bark 推送地址中最后一段"></label>' +
        '<label>通知分组（可选）<input name="bark_group" value="' + esc(st.bark_group) + '" placeholder="iOS 自动签名"></label>' +
        '<label>提示音（可选）<input name="bark_sound" value="' + esc(st.bark_sound) + '" placeholder="default"></label>' +
        '<div class="settings-actions">' +
          '<button type="submit" class="primary">保存设置</button>' +
          '<button type="button" id="bark-test">发送测试推送</button>' +
        '</div>' +
      '</form>';
    $('#settings-form').onsubmit = async function (e) {
      e.preventDefault();
      var f = e.target;
      try {
        var keep = Number(f.keep_versions.value) || 2;
        if (keep < 1) keep = 1;
        if (keep > 50) keep = 50;
        var out = await api('/api/settings', json('PUT', {
          site_title: f.site_title.value,
          bark_enabled: f.bark_enabled.checked,
          bark_server: f.bark_server.value,
          bark_key: f.bark_key.value,
          bark_group: f.bark_group.value,
          bark_sound: f.bark_sound.value,
          keep_versions: keep
        }));
        applySiteTitle(out.site_title);
        toast('设置已保存');
      } catch (err) { fail(err); }
    };
    $('#bark-test').onclick = async function () {
      var btn = this;
      btn.disabled = true;
      try {
        await api('/api/settings/bark/test', json('POST', {}));
        toast('测试推送已发送，请查看手机通知');
      } catch (err) { fail(err); }
      btn.disabled = false;
    };
  }

  // ---------- install (account scoped, shown as a tab) ----------
  loaders.install = async function () {
    renderInstall(await api('/api/install/apps'));
  };

  function renderInstall(list) {
    var head = '<p class="section-kicker">INSTALL</p><h2>安装</h2>' +
      '<div class="hint">仅显示当前账号可安装的应用。请用 iPhone 相机扫描二维码，或在 iOS Safari 中打开安装页。</div>';
    if (!list.length) {
      $('#panel-install').innerHTML = head +
        '<div class="empty-state">暂无可安装的应用。请先在「应用与配置」中启用自动签名并上传证书。</div>';
      return;
    }
    list.sort(function (a, b) { return new Date(b.updated_at) - new Date(a.updated_at); });
    var cards = list.map(function (a) {
      var icon = a.icon_url ? '<img src="' + esc(a.icon_url) + '" alt="">'
        : '<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="">';
      return '<div class="card">' +
        '<div class="card-head">' + icon + '<div><div class="name" data-i18n-skip>' + esc(a.name) + '</div>' +
          '<div class="sub mono">' + esc(a.bundle_id) + '</div></div></div>' +
        '<div class="install-meta">' +
          '<div>版本 <b>' + esc(a.version) + '</b></div>' +
          '<div>更新时间 <b>' + esc(fmtTime(a.updated_at)) + '</b></div>' +
          '<div>大小 <b>' + esc(fmtSize(a.size)) + '</b></div>' +
        '</div>' +
        '<div class="inline install-actions">' +
          '<a class="button button-primary" href="' + esc(a.install_url) + '">安装</a>' +
          '<button class="button button-qr" type="button" data-qr="' + esc(a.id) + '">' +
            '<span class="qr-mini" aria-hidden="true"></span>二维码</button>' +
        '</div></div>';
    }).join('');
    $('#panel-install').innerHTML = head + '<div class="card-grid" style="margin-top:18px">' + cards + '</div>';
    $$('#panel-install button[data-qr]').forEach(function (b) {
      b.onclick = function () {
        var item = list.find(function (x) { return x.id === b.dataset.qr; });
        if (item) openQrDialog(item);
      };
    });
  }

  // QR is generated locally (qrcode.js) so it works on an offline LAN.
  function makeQrDataURL(text) {
    if (typeof window.qrcode !== 'function') {
      throw new Error('二维码组件未加载');
    }
    var lib = window.qrcode;
    if (lib.stringToBytesFuncs && lib.stringToBytesFuncs['UTF-8']) {
      lib.stringToBytes = lib.stringToBytesFuncs['UTF-8'];
    }
    var qr = lib(0, 'M');
    qr.addData(text, 'Byte');
    qr.make();
    var n = qr.getModuleCount(), scale = 6, margin = 4;
    var size = (n + margin * 2) * scale;
    var canvas = document.createElement('canvas');
    canvas.width = canvas.height = size;
    var ctx = canvas.getContext('2d');
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, size, size);
    ctx.fillStyle = '#1d1d1f';
    for (var r = 0; r < n; r++) {
      for (var c = 0; c < n; c++) {
        if (qr.isDark(r, c)) {
          ctx.fillRect((c + margin) * scale, (r + margin) * scale, scale, scale);
        }
      }
    }
    return canvas.toDataURL('image/png');
  }

  function closeQrDialog() {
    var dlg = $('#qrDialog');
    if (typeof dlg.close === 'function' && dlg.open) dlg.close();
    else dlg.removeAttribute('open');
  }

  function openQrDialog(app) {
    try {
      $('#qrImage').src = makeQrDataURL(app.install_url || '');
    } catch (e) {
      fail(e);
      return;
    }
    $('#qrImage').alt = (app.name || '应用') + ' 的安装二维码';
    $('#qrAppName').textContent = app.name || '';
    $('#qrAppVersion').textContent = '版本 ' + (app.version || '-');
    $('#qrCopyLink').onclick = async function () {
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) {
          await navigator.clipboard.writeText(app.install_url || '');
        } else {
          var ta = document.createElement('textarea');
          ta.value = app.install_url || '';
          document.body.appendChild(ta);
          ta.select();
          document.execCommand('copy');
          document.body.removeChild(ta);
        }
        toast('安装链接已复制');
      } catch (e) { fail(e); }
    };
    var dlg = $('#qrDialog');
    if (typeof dlg.showModal === 'function') {
      if (!dlg.open) dlg.showModal();
    } else {
      dlg.setAttribute('open', '');
    }
  }

  function bindQrDialog() {
    $('#closeQrButton').onclick = closeQrDialog;
    $('#qrDialog').addEventListener('click', function (e) {
      if (e.target === $('#qrDialog')) closeQrDialog();
    });
  }

  // ---------- icon preview dialog ----------
  function closeIconDialog() {
    var dlg = $('#iconDialog');
    if (typeof dlg.close === 'function' && dlg.open) dlg.close();
    else dlg.removeAttribute('open');
  }

  function openIconPreview(icon) {
    var img = $('#iconPreviewImage');
    img.src = icon.url || '';
    img.alt = (icon.label || '图标') + ' 预览';
    $('#iconPreviewName').textContent = icon.label || '未命名图标';
    $('#iconPreviewUrl').textContent = icon.url || '';
    $('#iconOpenLink').href = icon.url || '#';
    var dlg = $('#iconDialog');
    if (typeof dlg.showModal === 'function') {
      if (!dlg.open) dlg.showModal();
    } else {
      dlg.setAttribute('open', '');
    }
  }

  function bindIconDialog() {
    $('#closeIconButton').onclick = closeIconDialog;
    $('#iconDialog').addEventListener('click', function (e) {
      if (e.target === $('#iconDialog')) closeIconDialog();
    });
  }

  // ---------- delegated actions ----------
  document.addEventListener('click', async function (e) {
    var btn = e.target.closest('button[data-action]');
    if (!btn) return;
    var id = btn.dataset.id;
    try {
      switch (btn.dataset.action) {
        case 'run-sync': {
          btn.disabled = true;
          try {
            var rr = await api('/api/sync/run', json('POST', {}));
            var msg = '立即执行完成：同步 ' + (rr.sources || 0) + ' 个源，新增 ' +
              (rr.new_versions || 0) + ' 个版本，创建 ' + (rr.jobs_created || 0) + ' 个任务';
            var ups = rr.updates || [];
            if (ups.length) {
              msg += '（' + ups.slice(0, 2).map(function (u) {
                return u.app_name + ' ' + u.version;
              }).join('、') + (ups.length > 2 ? ' 等' : '') + '）';
            }
            if ((rr.errors || []).length) msg += '；' + rr.errors.length + ' 个源失败';
            toast(msg);
          } finally {
            btn.disabled = false;
          }
          await loaders.sources();
          break;
        }
        case 'refresh-source': {
          var r = await api('/api/sources/' + encodeURIComponent(id) + '/refresh', json('POST', {}));
          toast('同步完成，新增 ' + (r.sync.new_versions || 0) + ' 个版本，创建 ' + r.jobs_created + ' 个任务');
          await loaders.sources();
          break;
        }
        case 'toggle-source': {
          var sources = await api('/api/sources');
          var src = sources.find(function (x) { return x.id === id; });
          if (!src) throw new Error('订阅源不存在');
          await api('/api/sources/' + encodeURIComponent(id), json('PUT', {
            name: src.name, url: src.url,
            enabled: !src.enabled,
            interval_minutes: src.interval_minutes
          }));
          toast(src.enabled ? '已停用该订阅源' : '已启用该订阅源');
          await loaders.sources();
          break;
        }
        case 'delete-source':
          if (!confirm(tr('删除该订阅源及其元数据？'))) return;
          await api('/api/sources/' + encodeURIComponent(id), { method: 'DELETE' });
          await loaders.sources();
          break;
        case 'sign-app': {
          var j = await api('/api/apps/' + encodeURIComponent(id) + '/sign', json('POST', {}));
          toast('已创建签名任务 ' + j.job_id);
          break;
        }
        case 'delete-app-config':
          if (!confirm(tr('删除该应用的配置？应用与已发布产物会保留，可在「全部」中重新配置。'))) return;
          await api('/api/apps/' + encodeURIComponent(id) + '/config', { method: 'DELETE' });
          toast(tr('已删除配置'));
          await loaders.apps();
          break;
        case 'delete-icon':
          await api('/api/icons/' + encodeURIComponent(id), { method: 'DELETE' });
          await loaders.icons();
          break;
        case 'delete-cert':
          if (!confirm(tr('删除该证书？已签名的应用不受影响。'))) return;
          await api('/api/certificates/' + encodeURIComponent(id), { method: 'DELETE' });
          await loaders.certs();
          break;
        case 'delete-artifact': {
          if (!confirm(tr('删除该产物？对应的安装入口与安装包也会一并移除。'))) return;
          await api('/api/artifacts/' + encodeURIComponent(id), { method: 'DELETE' });
          toast(tr('已删除产物'));
          await loaders.jobs();
          break;
        }
      }
    } catch (err) { fail(err); }
  });

  // ---------- boot ----------
  (async function boot() {
    // Bind auth forms unconditionally: a form that is visible for any reason
    // must never fall back to a native GET submit.
    bindAuth();
    bindQrDialog();
    bindIconDialog();
    try {
      me = await api('/api/me');
    } catch (e) {
      $('#app').hidden = true;
      $('#auth-view').hidden = false;
      return;
    }
    $('#auth-view').hidden = true;
    $('#app').hidden = false;
    // Only the account name is shown; repeating the role produced "admin · admin".
    $('#who').textContent = me.username;
    try { pub = await api('/api/config/public'); } catch (e) { pub = {}; }
    applySiteTitle(pub.site_title);
    if (pub.version) {
      var badge = $('#app-version');
      if (badge) badge.textContent = pub.version;
    }
    $('#logout').onclick = async function () {
      await api('/api/auth/logout', json('POST', {}));
      location.reload();
    };
    $$('.tab').forEach(function (b) {
      b.onclick = function () { switchTab(b.dataset.tab); };
    });
    var initialTab = 'overview';
    try {
      var remembered = sessionStorage.getItem('ias-active-tab');
      if (remembered && $$('.tab').some(function (b) { return b.dataset.tab === remembered; })) {
        initialTab = remembered;
      }
    } catch (e) { /* storage may be unavailable */ }
    await switchTab(initialTab);
  })();
})();
