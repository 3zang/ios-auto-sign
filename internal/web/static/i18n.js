/* Global UI language switch (Simplified Chinese <-> English).
 *
 * Loaded synchronously in <head> like theme.js. The dictionary is keyed by the
 * original Chinese copy, so templates in app.js / install.html stay untouched.
 * A MutationObserver translates text and a few attributes after every dynamic
 * render; switching language persists the choice and reloads the page so every
 * panel is re-rendered from its Chinese source text in one clean pass.
 */
(function () {
  'use strict';

  var KEY = 'ias-lang';
  var ATTRS = ['placeholder', 'title', 'aria-label', 'alt'];

  var EN = {
    // ---- brand / auth ----
    'iOS 自动签名': 'iOS Auto Sign',
    'iOS 自动签名控制台': 'iOS Auto Sign Console',
    '签名、分发，一站式控制台': 'Signing and distribution in one console',
    '账号操作': 'Account',
    '登录': 'Sign in',
    '注册': 'Register',
    '用户名': 'Username',
    '请输入用户名': 'Enter username',
    '密码': 'Password',
    '请输入密码': 'Enter password',
    '请输入账号密码以继续。': 'Enter your credentials to continue.',
    '首个注册的账号将成为管理员，密码至少 8 位。': 'The first account becomes admin; password must be at least 8 characters.',
    '请在 iOS 设备的 Safari 中打开安装页完成安装': 'Open the install page in Safari on your iOS device',
    '退出': 'Sign out',
    '控制台': 'Console',
    '登录控制台': 'Sign in to console',

    // ---- theme switch labels ----
    'GitHub 仓库': 'GitHub repository',
    'GitHub 发布页': 'GitHub Releases',

    // ---- navigation ----
    '概览': 'Overview',
    '订阅源': 'Sources',
    '应用与配置': 'Apps & Config',
    '图标库': 'Icon Library',
    '证书': 'Certificates',
    '任务与产物': 'Jobs & Artifacts',
    '安装': 'Install',
    '日志': 'Logs',
    '设置中心': 'Settings',
    '用户': 'Users',
    '已注册用户': 'Registered users',
    '下次检查': 'Next check',
    '待首次检查': 'Awaiting first check',
    '我的': 'My',
    '默认': 'Default',
    '已恢复为软件源默认间隔': 'Reverted to source default interval',
    '恢复为软件源默认间隔': 'Revert to source default',
    '角色': 'Role',
    '管理员': 'Admin',
    '普通用户': 'User',
    '注册时间': 'Registered',

    // ---- log viewer ----
    '运行日志': 'Server log',
    '信息': 'Info',
    '警告': 'Warning',
    '自动刷新': 'Auto refresh',
    '清空视图': 'Clear view',
    '仅管理员可查看服务器日志': 'Only administrators can view the server log',
    '仅管理员可查看用户列表': 'Only administrators can view the user list',

    // ---- source refresh interval ----
    '间隔': 'Interval',
    '分钟': 'min',
    '刷新间隔已更新': 'Refresh interval updated',

    // ---- dialogs ----
    '关闭二维码': 'Close QR code',
    '扫码安装': 'Scan to install',
    '安装二维码': 'Install QR code',
    '使用 iPhone 相机扫描二维码，在打开的提示中确认安装。二维码内容为该应用的 OTA 安装链接。':
      'Scan with the iPhone camera and confirm the prompt. The QR code contains the OTA install link for this app.',
    '使用 iPhone 相机扫描二维码，在打开的提示中确认安装。':
      'Scan with the iPhone camera and confirm the prompt.',
    '复制安装链接': 'Copy install link',
    '关闭预览': 'Close preview',
    '图标预览': 'Icon preview',
    '打开原图': 'Open original',
    '二维码组件未加载': 'QR code component not loaded',
    '已复制链接': 'Link copied',
    '安装链接已复制': 'Install link copied',
    '二维码': 'QR Code',

    // ---- overview ----
    '让每一次更新，': 'Every update,',
    '自动签好名。': 'signed automatically.',
    '订阅软件源一有新版本，平台就按你设定的名称、图标与证书自动重签名，发布安装包并推送提醒。从同步到安装，全程无需手动操作。':
      'When a source publishes a new version, the platform re-signs it with your name, icon and certificate, publishes the IPA and sends a notification. From sync to install, nothing is manual.',
    '订阅源自动轮询': 'Automatic source polling',
    '自定义名称与图标': 'Custom name and icon',
    '证书加密存储': 'Encrypted certificate storage',
    'Bark 更新提醒': 'Bark update alerts',
    '二维码扫码安装': 'QR code install',
    '版本自动保留': 'Automatic version retention',
    '配置应用': 'Configure apps',
    '查看可安装应用': 'View installable apps',
    '服务概览': 'Service overview',
    '运行中': 'Running',
    '订阅源（启用 / 全部）': 'Sources (enabled / total)',
    '应用（已配置 / 全部）': 'Apps (configured / total)',
    '签名成功 / 失败': 'Signing succeeded / failed',
    '已发布产物': 'Published artifacts',
    '安装地址': 'Install URL',
    '生成模式': 'mode',
    '流程：订阅源刷新 → 勾选应用并保存配置 → 上传 p12 / 密码 / mobileprovision → 自动签名 → 安装页出现可安装版本。检测到新版本时会按应用推送 Bark 通知，签名任务记录仅保留最近 5 条。':
      'Flow: refresh a source → select apps and save config → upload p12 / password / mobileprovision → automatic signing → installable versions appear. New versions trigger per-app Bark alerts; only the latest 5 signing jobs are kept.',
    '每应用保留最近': 'Keep last',
    '个版本 · 每': 'versions per app · poll every',
    '分钟轮询一次。': 'minutes.',
    '签名程序：': 'Signing tool: ',
    '默认源：': 'Default source: ',
    '流程：订阅源刷新': 'Flow: refresh source',
    '勾选应用并保存配置': 'select apps and save config',
    '上传 p12 / 密码 / mobileprovision': 'upload p12 / password / mobileprovision',
    '自动签名': 'automatic signing',
    '安装页出现可安装版本。检测到新版本时会按应用推送 Bark 通知，签名任务记录仅保留最近 5 条。':
      'installable versions appear on the Install page. New versions trigger a per-app Bark alert; only the latest 5 signing jobs are kept.',
    '启用自动签名': 'Enable automatic signing',

    // ---- sources ----
    '刷新': 'Refresh',
    '删除': 'Delete',
    '启用': 'Enable',
    '停用': 'Disable',
    '已启用': 'Enabled',
    '已停用': 'Disabled',
    '可用': 'Available',
    '分钟': 'min',
    '订阅软件源': 'Software sources',
    '立即执行': 'Run now',
    '点击「立即执行」会立即检查所有启用的软件源；发现新版本时自动创建签名任务，并推送 Bark 更新通知。':
      'Run now checks every enabled source immediately. New versions create signing jobs and trigger Bark notifications.',
    '点击「立即执行」会立即检查所有启用的软件源': 'Run now checks every enabled source immediately',
    '发现新版本时自动创建签名任务，并推送 Bark 更新通知。': 'New versions create signing jobs and trigger Bark notifications.',
    '名称': 'Name',
    '我的软件源': 'My source',
    '地址': 'URL',
    '轮询（分钟）': 'Poll interval (min)',
    '添加并同步': 'Add & sync',
    '状态': 'Status',
    '间隔': 'Interval',
    '上次检查': 'Last checked',
    '错误': 'Error',
    '操作': 'Actions',
    '暂无订阅源': 'No sources yet',
    '软件源': 'Source',
    '已添加，后台正在同步元数据': 'Added; syncing metadata in background',
    '同步完成，新增': 'Sync complete,',
    '个版本，创建': 'new versions,',
    '个任务': 'jobs created',
    '订阅源不存在': 'Source not found',
    '已停用该订阅源': 'Source disabled',
    '已启用该订阅源': 'Source enabled',
    '删除该订阅源及其元数据？': 'Delete this source and its metadata?',
    '立即执行完成：同步': 'Run complete: synced',
    '个源，新增': 'sources,',
    '等': 'etc.',
    '个源失败': 'source(s) failed',

    // ---- apps ----
    '应用与签名配置': 'Apps & signing configuration',
    '应用筛选': 'App filter',
    '已配置': 'Configured',
    '全部': 'All',
    '排序方式': 'Sort by',
    '排序': 'Sort',
    '按时间': 'By date',
    '按名称': 'By name',
    '自定义图标会写入 IPA（zsign -I），因此安装到设备后的桌面图标也会改变。勾选启用并选择证书后，检测到新版本时会自动创建签名任务。':
      'Custom icons are written into the IPA (zsign -I), so the home-screen icon changes too. After enabling and selecting a certificate, new versions create signing jobs automatically.',
    '还没有已配置的应用，切换到「全部」勾选并保存配置。': 'No configured apps yet. Switch to All, enable one and save.',
    '暂无应用，请先在「订阅源」中刷新元数据。': 'No apps yet. Refresh a source first.',
    '暂无版本': 'No versions',
    '来自': 'From',
    '未知源': 'Unknown source',
    '版本': 'Version',
    '配置签名元数据': 'Configure signing metadata',
    '显示名称': 'Display name',
    '图标来源': 'Icon source',
    '订阅源原始图标': 'Original source icon',
    '管理员图标库': 'Admin icon library',
    '自定义 URL': 'Custom URL',
    '选择图标': 'Choose icon',
    '未选择': 'Not selected',
    '图标预览': 'Icon preview',
    '删除配置': 'Delete config',
    '已删除配置': 'Configuration deleted',
    '删除该应用的配置？应用与已发布产物会保留，可在「全部」中重新配置。': 'Delete this app configuration? The app and published artifacts are kept; you can configure it again under "All".',
    '自定义图标 URL': 'Custom icon URL',
    '证书（p12 + 描述文件）': 'Certificate (p12 + provision)',
    '（缺描述文件）': ' (missing provision)',
    '.p12 文件': '.p12 file',
    'JSON 地址': 'JSON URL',
    '包名覆盖': 'Bundle ID override',
    '留空则使用': 'Leave empty to use',
    '压缩级别（0-9，留空继承全局）': 'Compression level (0-9, empty = global)',
    '强制签名（-f）': 'Force signing (-f)',
    '模式（不使用证书）': 'mode (no certificate)',
    '保存配置': 'Save config',
    '立即签名': 'Sign now',
    '配置已保存': 'Configuration saved',
    '缺描述文件': 'Missing provision',
    '预览图标': 'Preview icon',
    '预览': 'Preview',
    '已创建签名任务': 'Signing job created',
    '图标': 'Icon',
    '未命名图标': 'Unnamed icon',

    // ---- icon library ----
    '备用图标 A': 'Alternate icon A',
    '图标 URL（建议 HTTPS PNG）': 'Icon URL (HTTPS PNG recommended)',
    '实时预览': 'Live preview',
    '添加': 'Add',
    '图标库由管理员维护，你可在「应用与配置」中选用。点击缩略图可放大预览。':
      'The icon library is maintained by admins. Choose one in Apps & Config; click a thumbnail to enlarge.',
    '暂无图标': 'No icons yet',
    '图标已添加': 'Icon added',

    // ---- certificates ----
    '三件套完整': 'Complete set',
    '签名证书': 'Signing certificates',
    '正常重签名需要三件套：.p12 证书、证书密码、.mobileprovision 描述文件。三者均使用 AES-256-GCM 加密存储，接口不会返回明文。':
      'Normal re-signing needs three files: .p12, certificate password and .mobileprovision. All are stored with AES-256-GCM encryption and never returned in plain text.',
    '个人开发者证书': 'Personal developer certificate',
    '证书密码': 'Certificate password',
    '文件': 'file',
    '上传并加密保存': 'Upload & encrypt',
    '上传时间': 'Uploaded',
    '暂无证书': 'No certificates yet',
    '我的证书': 'My certificate',
    '证书已加密保存': 'Certificate encrypted and saved',
    '删除该证书？已签名的应用不受影响。': 'Delete this certificate? Already signed apps are unaffected.',

    // ---- jobs & artifacts ----
    '个版本）': 'versions)',
    '应用': 'App',
    '签名时间': 'Signed at',
    '大小': 'Size',
    '下载': 'Download',
    '暂无产物': 'No artifacts yet',
    '签名任务（仅保留最近 5 条）': 'Signing jobs (latest 5 only)',
    '任务 ID': 'Job ID',
    '创建': 'Created',
    '完成': 'Finished',
    '暂无任务': 'No jobs yet',

    // ---- settings ----
    '当前站点标题：': 'Current site title:',
    '全局设置（标题、Bark 通知）仅管理员可修改。': 'Global settings (title, Bark) can only be changed by an admin.',
    '主题可在右上角的开关随时切换，偏好保存在当前浏览器。': 'Use the switch in the top right to change theme; the preference is stored in this browser.',
    '顶部标题': 'Site title',
    '自定义控制台左上角显示的名称，保存后立即生效。': 'Customize the name shown at the top left; changes apply immediately.',
    '站点标题': 'Site title',
    '更新通知': 'Update notifications',
    '订阅源检测到新版本时，按应用推送一条「软件有更新」通知。首次添加软件源不会推送历史版本。':
      'When a new version is detected, a per-app "app updated" notification is sent. Adding a source for the first time does not replay historical versions.',
    '启用 Bark 推送': 'Enable Bark notifications',
    '服务器地址': 'Server URL',
    '设备 Key': 'Device key',
    '推送地址中最后一段': 'Last segment of the push URL',
    '通知分组（可选）': 'Notification group (optional)',
    '提示音（可选）': 'Sound (optional)',
    '保存设置': 'Save settings',
    '发送测试推送': 'Send test notification',
    '设置已保存': 'Settings saved',
    '测试推送已发送，请查看手机通知': 'Test notification sent, check your phone',

    // ---- install ----
    '应用安装': 'App installation',
    '请在 iOS 设备的 Safari 中打开本页；仅显示当前登录账号可安装的应用。':
      'Open this page in Safari on your iOS device. Only apps available to the signed-in account are listed.',
    '请在 iOS 设备的 Safari 中打开本页': 'Open this page in Safari on your iOS device',
    '仅显示当前登录账号可安装的应用。': 'Only apps available to the signed-in account are listed.',
    '仅显示当前账号可安装的应用。请用 iPhone 相机扫描二维码，或在 iOS Safari 中打开安装页。':
      'Only apps available to this account are listed. Scan the QR code with the iPhone camera or open the install page in iOS Safari.',
    '暂无可安装的应用。请先在控制台勾选应用、上传证书并等待签名任务完成。':
      'No installable apps yet. Select an app in the console, upload a certificate and wait for signing to finish.',
    '暂无可安装的应用。请先在「应用与配置」中启用自动签名并上传证书。':
      'No installable apps yet. Enable automatic signing in Apps & Config and upload a certificate.',
    '安装数据已按账号隔离，请先': 'Install data is account-scoped. Please ',
    '后再打开本页。': ' first, then reopen this page.',
    '安装链接使用 itms-services 协议，请在 iOS 设备的 Safari 中打开；桌面浏览器点击不会触发安装。':
      'Install links use the itms-services scheme. Open them in Safari on an iOS device; desktop browsers will not trigger installation.',
    '安装链接使用 itms-services 协议，请在 iOS 设备的 Safari 中打开': 'Install links use itms-services; open them in Safari on an iOS device',
    '桌面浏览器点击不会触发安装。': 'Desktop browsers will not trigger installation.',
    '更新时间': 'Updated',
    '的安装二维码': ' install QR code',
    '点击缩略图可放大预览': 'Click a thumbnail to enlarge',

    // ---- version detail / artifacts / settings (added) ----
    '更新详情': 'Update details',
    '更多': 'More',
    '收起': 'Collapse',
    '删除该产物？对应的安装入口与安装包也会一并移除。': 'Delete this artifact? Its install entry and IPA are removed too.',
    '已删除产物': 'Artifact deleted',
    '上一页': 'Previous',
    '下一页': 'Next',
    '第': 'Page',
    '页': 'pages',
    '共': 'Total',
    '条': 'items',
    '每页': 'Per page',
    '个版本': 'versions',
    '版本保留': 'Version retention',
    '每应用保留版本数': 'Versions kept per app',
    '每个应用最多保留的已签名版本数量；超过后最旧的版本会被自动清理，对应安装入口一并移除。': 'Maximum signed versions kept per app; older ones are auto-removed with their install entries.',
    '版本自动保留策略由管理员配置。': 'The version-retention policy is set by the administrator.'
  };

  var KEYS = Object.keys(EN).sort(function (a, b) { return b.length - a.length; });
  var CJK = /[\u3400-\u9fff]/;

  // Only long, unambiguous phrases may be replaced inside a longer sentence.
  // Short labels are translated by exact match only, so "安装" never turns
  // "安装数据已按账号隔离" into a half-translated string.
  var SHORT_PARTIAL = ['已配置', '全部', '版本', '分钟', '大小', '预览', '个任务', '等', '控制台', '文件', '更多', '收起', '个版本', '第', '共', '条', '上一页', '下一页', '每页', '页'];
  var PARTIAL = KEYS.filter(function (k) {
    return k.length >= 4 || SHORT_PARTIAL.indexOf(k) >= 0;
  });

  function readLang() {
    try {
      var v = localStorage.getItem(KEY);
      if (v === 'en' || v === 'zh') return v;
    } catch (e) { /* storage may be unavailable */ }
    return 'zh';
  }

  var lang = readLang();

  function has(k) { return Object.prototype.hasOwnProperty.call(EN, k); }

  // Exact labels first (preserving surrounding whitespace), then only safe
  // long phrases inside mixed strings such as "每应用保留最近 2 个版本".
  function t(s) {
    if (lang !== 'en' || !s || !CJK.test(s)) return s;
    var raw = String(s);
    var trimmed = raw.trim();
    if (has(trimmed)) {
      var at = raw.indexOf(trimmed);
      return raw.slice(0, at) + EN[trimmed] + raw.slice(at + trimmed.length);
    }
    var out = raw;
    for (var i = 0; i < PARTIAL.length; i++) {
      var k = PARTIAL[i];
      if (out.indexOf(k) === -1) continue;
      out = out.split(k).join(EN[k]);
    }
    return out;
  }

  function skipped(node) {
    var p = node.parentElement;
    while (p) {
      if (p.nodeType === 1) {
        if (p.hasAttribute('data-i18n-skip')) return true;
        if (/\bmono\b/.test(String(p.className || '')) && p.tagName !== 'BODY') return true;
        if (p.tagName === 'SCRIPT' || p.tagName === 'STYLE' || p.tagName === 'TEXTAREA') return true;
      }
      p = p.parentElement;
    }
    return false;
  }

  var seen = new WeakMap();

  function sweep() {
    if (lang !== 'en') return;
    var walker = document.createTreeWalker(document.documentElement, NodeFilter.SHOW_TEXT, null);
    var node;
    while ((node = walker.nextNode())) {
      if (skipped(node)) continue;
      var raw = node.nodeValue;
      if (!raw || !CJK.test(raw)) continue;
      if (seen.get(node) === raw) continue;
      var out = t(raw);
      if (out !== raw) { seen.set(node, out); node.nodeValue = out; }
    }
    var els = document.querySelectorAll('*');
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      if (el.hasAttribute('data-i18n-skip') || el.tagName === 'SCRIPT' || el.tagName === 'STYLE') continue;
      for (var j = 0; j < ATTRS.length; j++) {
        var a = ATTRS[j];
        var v = el.getAttribute(a);
        if (!v || !CJK.test(v)) continue;
        var tv = t(v);
        if (tv !== v) el.setAttribute(a, tv);
      }
    }
  }

  var timer = null;
  function schedule() {
    if (lang !== 'en' || timer) return;
    timer = setTimeout(function () { timer = null; sweep(); }, 24);
  }

  function start() {
    sweep();
    if (lang !== 'en') return;
    if (typeof MutationObserver === 'function') {
      new MutationObserver(schedule).observe(document.documentElement, {
        subtree: true, childList: true, characterData: true,
        attributes: true, attributeFilter: ATTRS
      });
    }
  }

  function currentTab() {
    var a = document.querySelector('.tab.active');
    return a ? a.dataset.tab : '';
  }

  function setLang(next) {
    try { localStorage.setItem(KEY, next); } catch (e) { /* ignore */ }
    try {
      var tab = currentTab();
      if (tab) sessionStorage.setItem('ias-active-tab', tab);
    } catch (e) { /* ignore */ }
    location.reload();
  }

  function bind() {
    var btns = document.querySelectorAll('.lang-switch');
    for (var i = 0; i < btns.length; i++) {
      (function (btn) {
        btn.textContent = lang === 'zh' ? 'EN' : '中';
        var label = lang === 'zh' ? '切换到英文' : 'Switch to Chinese';
        btn.title = label;
        btn.setAttribute('aria-label', label);
        btn.addEventListener('click', function () { setLang(lang === 'zh' ? 'en' : 'zh'); });
      })(btns[i]);
    }
  }

  document.documentElement.lang = lang === 'en' ? 'en' : 'zh-CN';
  document.documentElement.setAttribute('data-lang', lang);

  window.IASI18N = {
    t: t,
    get lang() { return lang; },
    sweep: sweep,
    setLang: setLang
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { bind(); start(); });
  } else {
    bind();
    start();
  }
})();
