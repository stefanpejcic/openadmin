// Need Help? panel, renders the page's docs straight from the OpenPanel repo on GitHub
window.OPPageHelp = (function () {
  const RAW = 'https://raw.githubusercontent.com/stefanpejcic/OpenPanel/refs/heads/main/website/docs/';
  const SITE = 'https://openpanel.com';
  const cache = {};
  let libs;

  function loadScript(src) {
    return new Promise((resolve, reject) => {
      const s = document.createElement('script');
      s.src = src;
      s.onload = resolve;
      s.onerror = () => reject(new Error('failed to load ' + src));
      document.head.appendChild(s);
    });
  }

  function loadLibs() {
    libs = libs || Promise.all([
      window.marked ? null : loadScript('/static/vendor/marked/marked.min.js'),
      window.DOMPurify ? null : loadScript('/static/vendor/dompurify/purify.min.js'),
    ]);
    return libs;
  }

  // front matter out, docusaurus :::tip blocks become blockquotes with a bold title
  function prepare(md) {
    md = md.replace(/^---\n[\s\S]*?\n---\n/, '');
    md = md.replace(/^import .*$/gm, '');
    return md.replace(/^:::(\w+)[ \t]*(.*)\n([\s\S]*?)^:::[ \t]*$/gm, (_, type, title, body) => {
      const head = '**' + (title || type.charAt(0).toUpperCase() + type.slice(1)) + '**';
      return [head, ...body.trimEnd().split('\n')].map(l => '> ' + l).join('\n') + '\n';
    });
  }

  // "until:X" keeps what's before the X heading, "from:X" keeps the X section up to the next heading of the same level
  function pick(md, part) {
    const m = /^(until|from):(.+)$/.exec(part || '');
    if (!m) return md;
    const lines = md.split('\n');
    const heading = l => /^(#{1,6})\s+(.*?)\s*$/.exec(l);
    let fence = false, start = -1, level = 0;
    for (let i = 0; i < lines.length; i++) {
      if (/^\s*(```|~~~)/.test(lines[i])) fence = !fence;
      const h = !fence && heading(lines[i]);
      if (!h) continue;
      if (start < 0 && h[2].toLowerCase() === m[2].toLowerCase()) {
        if (m[1] === 'until') return lines.slice(0, i).join('\n');
        start = i;
        level = h[1].length;
      } else if (start >= 0 && h[1].length <= level) {
        return lines.slice(start, i).join('\n');
      }
    }
    return start >= 0 ? lines.slice(start).join('\n') : md;
  }

  function absolute(url) {
    return url.startsWith('/') && !url.startsWith('//') ? SITE + url : url;
  }

  function render(el, md, part) {
    const html = DOMPurify.sanitize(marked.parse(pick(prepare(md), part)));
    const tpl = document.createElement('template');
    tpl.innerHTML = html;
    const dark = document.documentElement.classList.contains('dark');
    tpl.content.querySelectorAll('img').forEach(img => {
      const src = img.getAttribute('src') || '';
      // docs ship light/dark screenshot pairs, keep the one matching the theme
      if (src.includes('#gh-dark-mode-only') && !dark) return img.remove();
      if (src.includes('#gh-light-mode-only') && dark) return img.remove();
      img.src = absolute(src.split('#')[0]);
      img.loading = 'lazy';
    });
    tpl.content.querySelectorAll('a[href]').forEach(a => {
      const href = a.getAttribute('href');
      if (href.startsWith('#')) return;
      a.href = absolute(href.replace(/\.mdx?(#|$)/, '/$1'));
      a.target = '_blank';
      a.rel = 'noopener';
    });
    el.replaceChildren(tpl.content);
  }

  async function load(el, doc, part) {
    const key = doc + '|' + (part || '');
    if (!el || !doc || el.dataset.loaded === key) return;
    try {
      cache[doc] = cache[doc] || fetch(RAW + doc + '.md').then(r => {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.text();
      });
      const [md] = await Promise.all([cache[doc], loadLibs()]);
      render(el, md, part);
      el.dataset.loaded = key;
    } catch (e) {
      delete cache[doc];
      el.textContent = 'Could not load the documentation right now, use the link above to open it on openpanel.com.';
    }
  }

  return { load };
})();
