// WebSocket client: connects to /ws and renders the latest value per subject.
// Time cells carry data-iso so browser-timezone.js can re-format them on toggle.
// Rows carry data-topic / data-ts so the table can be sorted by topic or time.
(function () {
    const rows = document.getElementById("rows");
    const status = document.getElementById("status");
    const filterInput = document.getElementById("topic-filter");
    const filterCount = document.getElementById("filter-count");
    const seen = new Map(); // subject -> <tr>

    // --- sorting -------------------------------------------------------------
    // Default to newest-first (time descending) when there's no saved preference.
    let sortKey = "time"; // "topic" | "time" | null
    let sortDir = -1;     // 1 = ascending, -1 = descending

    // Restore the persisted sort (stored as "<key>:<dir>", e.g. "time:-1").
    const storedSort = localStorage.getItem("sort");
    if (storedSort) {
        const [k, d] = storedSort.split(":");
        if ((k === "topic" || k === "time") && (d === "1" || d === "-1")) {
            sortKey = k;
            sortDir = Number(d);
        } else {
            localStorage.removeItem("sort"); // discard anything unexpected
        }
    }

    function resort() {
        if (!sortKey) return;
        const trs = Array.from(rows.children);
        trs.sort((a, b) => {
            if (sortKey === "time") {
                const av = Number(a.dataset.ts);
                const bv = Number(b.dataset.ts);
                const an = Number.isFinite(av) ? av : -Infinity; // missing ts sorts last (desc)
                const bn = Number.isFinite(bv) ? bv : -Infinity;
                if (an === bn) return 0; // avoid (-Inf - -Inf) = NaN comparator
                return (an - bn) * sortDir;
            }
            return (a.dataset.topic || "").localeCompare(b.dataset.topic || "") * sortDir;
        });
        for (const tr of trs) rows.appendChild(tr); // re-append in sorted order
    }

    // Coalesce re-sorts to one per animation frame on a busy feed.
    let resortScheduled = false;
    function scheduleResort() {
        if (resortScheduled) return;
        resortScheduled = true;
        requestAnimationFrame(() => {
            resortScheduled = false;
            resort();
            updateFilterCount(); // row set changed -> refresh "N of M" once per frame
        });
    }

    function updateIndicators() {
        // Both the table header buttons and the mobile sort buttons carry data-key.
        document.querySelectorAll("[data-key]").forEach((el) => {
            if (el.dataset.key === sortKey) {
                el.dataset.dir = sortDir > 0 ? "asc" : "desc";
            } else {
                delete el.dataset.dir;
            }
        });
        // Expose the current sort to assistive tech on the column headers.
        document.querySelectorAll("th.sortable").forEach((th) => {
            const btn = th.querySelector("[data-key]");
            const key = btn && btn.dataset.key;
            th.setAttribute(
                "aria-sort",
                key === sortKey ? (sortDir > 0 ? "ascending" : "descending") : "none"
            );
        });
    }

    function setSort(key) {
        if (sortKey === key) sortDir = -sortDir; // toggle direction on re-click
        else { sortKey = key; sortDir = 1; }
        localStorage.setItem("sort", sortKey + ":" + sortDir); // persist choice
        updateIndicators();
        resort();
    }

    document.querySelectorAll("[data-key]").forEach((el) => {
        el.addEventListener("click", () => setSort(el.dataset.key));
    });

    updateIndicators(); // reflect any restored sort on the headers/buttons at load

    // --- filtering -----------------------------------------------------------
    // Case-insensitive substring match on the topic label. Purely client-side:
    // rows are hidden (not removed), so they keep updating live in the background
    // and reappear instantly when the filter is cleared. Deliberately not
    // persisted — a saved filter that hid most of the feed on the next visit
    // would be a footgun.
    let filterText = "";

    function matchesFilter(tr) {
        return !filterText || (tr.dataset.topic || "").toLowerCase().includes(filterText);
    }

    function updateFilterCount() {
        const total = seen.size;
        let text = total === 1 ? "1 topic" : total + " topics";
        if (filterText) {
            let shown = 0;
            for (const tr of rows.children) if (!tr.hidden) shown++;
            text = shown + " of " + text;
        }
        filterCount.textContent = text;
    }

    function applyFilter() {
        for (const tr of rows.children) tr.hidden = !matchesFilter(tr);
        updateFilterCount();
    }

    filterInput.addEventListener("input", () => {
        filterText = filterInput.value.trim().toLowerCase();
        applyFilter();
    });

    // Honor a value the browser may have restored across a reload, then show
    // the initial (empty-feed) count.
    filterText = filterInput.value.trim().toLowerCase();
    updateFilterCount();

    // --- staleness -----------------------------------------------------------
    // wsfeed's seed is last-value-per-subject from compacted Kafka topics with no age
    // bound, so a URL last visited weeks ago would still show up. Cap the root feed to
    // the last 7 days: drop stale rows from the seed and periodically sweep rows that
    // age out while the socket stays open. Rows without a ts can't be aged, so they're
    // left alone.
    const MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;
    function isStale(ts) {
        return ts != null && Date.now() - ts * 1000 > MAX_AGE_MS;
    }

    function dropRow(subject, tr) {
        tr.remove();
        seen.delete(subject);
    }

    // Sweep aged-out rows once a minute (covers rows that go stale live).
    setInterval(() => {
        let changed = false;
        for (const [subject, tr] of seen) {
            const ts = Number(tr.dataset.ts);
            if (Number.isFinite(ts) && isStale(ts)) {
                dropRow(subject, tr);
                changed = true;
            }
        }
        if (changed) updateFilterCount();
    }, 60000);

    // --- versions panel ------------------------------------------------------
    // cluster/versions is also a normal table row; this mirrors it into the fixed
    // "What's running" section so a visitor sees the versions without scrolling the feed.
    const versionsSection = document.getElementById("versions");
    const versionsList = document.getElementById("versions-list");
    const VERSION_GROUPS = [
        ["Kubernetes", (d) => d.kubernetes],
        ["Nodes", (d) => d.nodes],
        ["Platform", (d) => d.platform],
        ["Apps", (d) => d.apps],
        ["Go", (d) => d.go],
    ];
    function renderVersions(data) {
        if (!data || typeof data !== "object") return;
        versionsList.replaceChildren();
        for (const [label, pick] of VERSION_GROUPS) {
            const v = pick(data);
            if (v == null) continue;
            const dt = document.createElement("dt");
            dt.textContent = label;
            const dd = document.createElement("dd");
            if (typeof v === "object") {
                // "key value · key value", keys sorted; textContent only (never innerHTML with data).
                for (const k of Object.keys(v).sort()) {
                    const item = document.createElement("span");
                    item.className = "version-item";
                    const kEl = document.createElement("span");
                    kEl.className = "version-key";
                    kEl.textContent = k + " ";
                    const vEl = document.createElement("code");
                    vEl.textContent = String(v[k]);
                    item.append(kEl, vEl);
                    dd.appendChild(item);
                }
            } else {
                const vEl = document.createElement("code");
                vEl.textContent = String(v);
                dd.appendChild(vEl);
            }
            versionsList.append(dt, dd);
        }
        versionsSection.hidden = false;
    }

    // --- live feed -----------------------------------------------------------
    // Reconnect with capped exponential backoff + jitter. A fixed delay makes a
    // whole network of clients dropping together (e.g. behind one NAT) stampede on
    // reconnect; equal jitter spreads the retries out. Reset to the floor on a
    // successful open so a long-lived connection that later drops retries quickly.
    let retryDelay = 1000;
    const RETRY_MAX = 30000;
    function connect() {
        const proto = location.protocol === "https:" ? "wss" : "ws";
        const ws = new WebSocket(`${proto}://${location.host}/ws`);

        ws.onopen = () => { status.textContent = "connected"; retryDelay = 1000; };
        ws.onclose = () => {
            status.textContent = "disconnected — retrying…";
            const wait = retryDelay / 2 + Math.random() * (retryDelay / 2);
            retryDelay = Math.min(retryDelay * 2, RETRY_MAX);
            setTimeout(connect, wait);
        };
        ws.onmessage = (ev) => {
            const { subject, topic, ts, data } = JSON.parse(ev.data);
            // Ignore anything older than MAX_AGE_MS. This drops stale entries
            // from the last-per-subject seed; genuine live updates are always fresh.
            if (isStale(ts)) {
                const stale = seen.get(subject);
                if (stale) dropRow(subject, stale); // a live row that just aged out
                return;
            }
            let tr = seen.get(subject);
            if (!tr) {
                tr = document.createElement("tr");
                // Empty cells; filled via textContent below (never innerHTML with data).
                tr.innerHTML = '<td></td><td class="time"></td><td class="data"></td>';
                rows.appendChild(tr);
                seen.set(subject, tr);
            }
            // `topic` and `subject` are the same string from wsfeed today (`topic` is kept
            // in the envelope for compatibility); prefer topic, fall back to subject.
            const label = topic || subject;
            tr.children[0].textContent = label;
            tr.children[0].title = label; // full value on hover if truncated
            tr.dataset.topic = label;     // sort key
            tr.hidden = !matchesFilter(tr); // honor the active filter for new rows

            const timeCell = tr.querySelector(".time");
            if (ts != null) {
                // ts is epoch seconds (UTC) captured when ingest received the message.
                const iso = new Date(ts * 1000).toISOString();
                timeCell.dataset.iso = iso;
                const formatted = fmtTS(iso);
                timeCell.textContent = formatted;
                timeCell.title = tsTitle(formatted);
                tr.dataset.ts = String(ts); // sort key
            } else {
                delete timeCell.dataset.iso;
                timeCell.textContent = "";
                tr.dataset.ts = "";
            }

            tr.querySelector(".data").textContent = JSON.stringify(data);
            if (subject === "cluster/versions") renderVersions(data);
            scheduleResort(); // keep sorted order as values/timestamps change
        };
    }

    connect();
})();
