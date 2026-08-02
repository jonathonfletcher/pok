// Theme toggle: cycles Auto -> Light -> Dark, persisted in localStorage.
// Sets data-theme on <html> (removed for "auto", which follows the OS setting).
// Approach adapted from https://codeberg.org/jayblunt/esi-sso-quart/
(function () {
    const modes = ["auto", "light", "dark"];
    const labels = ["Auto", "Light", "Dark"];

    let stored = localStorage.getItem("theme");
    if (stored && !modes.includes(stored)) {
        localStorage.removeItem("theme");
        stored = null;
    }
    if (stored) document.documentElement.setAttribute("data-theme", stored);

    const btn = document.getElementById("theme-toggle");
    if (!btn) return;

    let current = Math.max(0, modes.indexOf(stored || "auto"));
    btn.textContent = labels[current];
    btn.setAttribute("aria-label", "Toggle theme, currently " + labels[current]);
    btn.addEventListener("click", function () {
        current = (current + 1) % modes.length;
        const mode = modes[current];
        btn.textContent = labels[current];
        btn.setAttribute("aria-label", "Toggle theme, currently " + labels[current]);
        if (mode === "auto") {
            document.documentElement.removeAttribute("data-theme");
            localStorage.removeItem("theme");
        } else {
            document.documentElement.setAttribute("data-theme", mode);
            localStorage.setItem("theme", mode);
        }
    });
})();
