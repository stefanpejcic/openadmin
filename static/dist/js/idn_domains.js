function decodeIDNDomains() {
    if (typeof punycode === 'undefined') return;

    document.querySelectorAll('.punycode').forEach(el => {
        const raw = el.textContent.trim();
        if (!raw.includes('xn--')) return;

        try {
            const decoded = punycode.toUnicode(raw);
            if (decoded !== raw) {
                el.textContent = decoded;
                if (!el.title) el.title = raw;
            }
        } catch (e) {
            // leave the punycode-encoded value as-is
        }
    });
}

window.addEventListener('load', decodeIDNDomains);
