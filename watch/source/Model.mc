import Toybox.Application;
import Toybox.Graphics;
import Toybox.Lang;

// Turns a relay snapshot into rows ready to draw. No drawing here.
//   snap = {v:1, t:<relay time>, rows:[{kind, label?, pct, reset, sev}]}
(:glance)
module Model {
    const MAX_ROWS = 3;
    const STALE_SEC = 900;

    function rowsFor(snap as Dictionary, now as Number, compact as Boolean) as Array<Dictionary> {
        var src = snap["rows"];
        var out = [] as Array<Dictionary>;
        if (!(src instanceof Array)) {
            return out;
        }
        var t = snap["t"];
        var stale = true;
        if (t instanceof Number) {
            stale = isStale(t, now);
        }
        for (var i = 0; i < src.size() && out.size() < MAX_ROWS; i++) {
            if (!(src[i] instanceof Dictionary)) {
                continue;
            }
            var r = src[i] as Dictionary;
            var reset = r["reset"] instanceof Number ? r["reset"] as Number : 0;
            var pct = r["pct"] instanceof Number ? r["pct"] as Number : 0;
            var sev = r["sev"] instanceof Number ? r["sev"] as Number : 0;
            // The window has reset even if the computer is off and sent nothing since.
            var resetDone = reset > 0 && reset <= now;
            if (resetDone) {
                pct = 0;
                sev = 0;
            }
            var kind = r["kind"] instanceof String ? r["kind"] as String : "?";
            out.add({
                :label => labelFor(kind, r["label"] as String?, compact),
                :short => labelFor(kind, r["label"] as String?, true),
                :pct => pct,
                :reset => reset,
                :resetDone => resetDone,
                :color => colorFor(pct, sev, stale)
            });
        }
        return out;
    }

    // One-line percent for the tightest screens: "9% · 2h"; "0%" once the window has reset.
    function compactPct(pct as Number, reset as Number, now as Number) as String {
        if (reset > 0 && reset <= now) {
            return "0%";
        }
        if (reset <= 0) {
            return pct + "%";
        }
        var m = (reset - now + 59) / 60;
        var h = m / 60;
        var d = h / 24;
        var left = d > 0 ? d + Ui.unit(Rez.Strings.UnitD) : (h > 0 ? h + Ui.unit(Rez.Strings.UnitH) : m + Ui.unit(Rez.Strings.UnitM));
        return pct + "% · " + left;
    }

    function labelFor(kind as String, label as String?, compact as Boolean) as String {
        if (kind.equals("session")) {
            return Application.loadResource(compact ? Rez.Strings.SessionShort : Rez.Strings.SessionFull) as String;
        }
        if (kind.equals("weekly_all")) {
            return Application.loadResource(compact ? Rez.Strings.WeekShort : Rez.Strings.WeekFull) as String;
        }
        return label != null ? label : kind;
    }

    function colorFor(pct as Number, sev as Number, stale as Boolean) as Number {
        if (stale) {
            return Graphics.COLOR_LT_GRAY;
        }
        if (sev >= 2 || pct >= 90) {
            return Graphics.COLOR_RED;
        }
        if (sev >= 1 || pct >= 75) {
            return Graphics.COLOR_ORANGE;
        }
        return Graphics.COLOR_BLUE;
    }

    function isStale(t as Number, now as Number) as Boolean {
        return now - t > STALE_SEC;
    }
}
