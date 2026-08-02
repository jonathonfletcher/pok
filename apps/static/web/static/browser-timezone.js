// Timezone toggle: cycles UTC -> browser-local, persisted in localStorage.
// Sets data-timezone on <html> (removed for the UTC default) and re-formats every
// element carrying data-iso. Approach from https://codeberg.org/jayblunt/esi-sso-quart/
(function () {
    const modes = ["utc", "local"];
    const labels = ["UTC", Intl.DateTimeFormat().resolvedOptions().timeZone];

    let stored = localStorage.getItem("timezone");
    if (stored && !modes.includes(stored)) {
        localStorage.removeItem("timezone");
        stored = null;
    }
    if (stored) document.documentElement.setAttribute("data-timezone", stored);

    const btn = document.getElementById("timezone-toggle");
    if (!btn) return;

    // Re-render all timestamps when the timezone changes.
    window.addEventListener("timezone-changed", () => {
        document.querySelectorAll("[data-iso]").forEach((el) => {
            const formatted = fmtTS(el.dataset.iso);
            el.textContent = formatted;
            el.title = tsTitle(formatted);
        });
    });

    // Keep the button width stable across labels to avoid layout jitter.
    const rootFontPx = parseFloat(getComputedStyle(document.documentElement).fontSize);
    const widths = labels.map((l) => {
        btn.textContent = l;
        return btn.offsetWidth;
    });
    let current = Math.max(0, modes.indexOf(stored || "utc"));
    // offsetWidth is a pixel measurement; express it in rem for unit consistency.
    btn.style.minWidth = Math.max(...widths) / rootFontPx + "rem";
    btn.textContent = labels[current];
    btn.setAttribute("aria-label", "Toggle timestamp timezone, currently " + labels[current]);

    btn.addEventListener("click", function () {
        current = (current + 1) % modes.length;
        const mode = modes[current];
        btn.textContent = labels[current];
        btn.setAttribute("aria-label", "Toggle timestamp timezone, currently " + labels[current]);
        if (mode === "utc") {
            document.documentElement.removeAttribute("data-timezone");
            localStorage.removeItem("timezone");
        } else {
            document.documentElement.setAttribute("data-timezone", mode);
            localStorage.setItem("timezone", mode);
        }
        window.dispatchEvent(new CustomEvent("timezone-changed", { detail: { mode } }));
    });
})();
