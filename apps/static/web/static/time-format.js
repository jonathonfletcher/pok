// Timezone-aware timestamp formatting shared by app.js and browser-timezone.js.
// Timestamps are UTC by default; the timezone toggle sets data-timezone="local".
// Approach adapted from https://codeberg.org/jayblunt/esi-sso-quart/
const _tsFormatters = {};

function getBrowserTimezone() {
    // undefined => browser-local; "UTC" => force UTC (the default).
    return document.documentElement.getAttribute("data-timezone") === "local"
        ? undefined
        : "UTC";
}

function getBrowserTimezoneLabel() {
    return getBrowserTimezone()
        ? "UTC"
        : Intl.DateTimeFormat().resolvedOptions().timeZone;
}

function getFormatter() {
    const tz = getBrowserTimezone();
    const key = tz || "local";
    if (!_tsFormatters[key]) {
        const options = {
            year: "numeric", month: "2-digit", day: "2-digit",
            hour: "2-digit", minute: "2-digit", second: "2-digit",
            hour12: false, timeZone: tz,
            // Show the zone on every timestamp — UTC as "UTC", local as its short
            // offset (e.g. GMT+1) — so a bare time is never ambiguous.
            timeZoneName: "short",
        };
        // "sv-SE" gives an ISO-ish YYYY-MM-DD HH:MM:SS layout for UTC.
        _tsFormatters[key] = new Intl.DateTimeFormat(tz ? "sv-SE" : undefined, options);
    }
    return _tsFormatters[key];
}

function fmtTS(iso) {
    if (!iso) return "";
    return getFormatter().format(new Date(iso));
}

function tsTitle(formatted) {
    // Tooltip carries the fuller zone identity (IANA name for local, "UTC" for
    // UTC). The formatted string already ends with the short zone, so skip the
    // suffix when it would just repeat it (e.g. "… UTC UTC").
    const label = getBrowserTimezoneLabel();
    return formatted.endsWith(label) ? formatted : formatted + " " + label;
}
