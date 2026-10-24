import Toybox.Application;
import Toybox.Graphics;
import Toybox.Lang;
import Toybox.Math;

// Formatting and drawing helpers shared by the glance and the main view.
(:glance)
module Ui {

    function unit(id as ResourceId) as String {
        return Application.loadResource(id) as String;
    }

    // Time until a reset: "1d 12h", "1h 15m", "15m".
    function until(epoch as Number, now as Number) as String {
        var s = epoch - now;
        var m = s <= 0 ? 0 : (s + 59) / 60;
        var h = m / 60;
        var d = h / 24;
        if (d > 0) {
            return d + unit(Rez.Strings.UnitD) + " " + (h % 24) + unit(Rez.Strings.UnitH);
        }
        if (h > 0) {
            return h + unit(Rez.Strings.UnitH) + " " + (m % 60) + unit(Rez.Strings.UnitM);
        }
        return (m < 1 ? 1 : m) + unit(Rez.Strings.UnitM);
    }

    // Age of a snapshot: "just now", "5m ago", "3h ago", "2d ago".
    function ago(t as Number, now as Number) as String {
        var s = now - t;
        if (s < 60) {
            return unit(Rez.Strings.JustNow);
        }
        var m = s / 60;
        var v = m < 60 ? m + unit(Rez.Strings.UnitM) : (m < 1440 ? (m / 60) + unit(Rez.Strings.UnitH) : (m / 1440) + unit(Rez.Strings.UnitD));
        return v + " " + unit(Rez.Strings.Ago);
    }

    function drawBar(dc as Graphics.Dc, x as Number, y as Number, w as Number, h as Number,
                     pct as Number, color as Number) as Void {
        var r = h / 2;
        dc.setColor(Graphics.COLOR_DK_GRAY, Graphics.COLOR_TRANSPARENT);
        dc.fillRoundedRectangle(x, y, w, h, r);
        if (pct <= 0) {
            return;
        }
        var fw = w * (pct > 100 ? 100 : pct) / 100;
        if (fw < h) {
            fw = h;
        }
        dc.setColor(color, Graphics.COLOR_TRANSPARENT);
        dc.fillRoundedRectangle(x, y, fw, h, r);
    }

    // Cuts text with "…" so it fits width; returns it unchanged when it already fits.
    function fit(dc as Graphics.Dc, text as String, font as Graphics.FontType, width as Number) as String {
        if (dc.getTextWidthInPixels(text, font) <= width) {
            return text;
        }
        var n = text.length();
        while (n > 1 && dc.getTextWidthInPixels(text.substring(0, n) + "…", font) > width) {
            n -= 1;
        }
        return text.substring(0, n) + "…";
    }

    // Percent text: white when the bar is the calm blue, otherwise the bar's colour.
    function pctColor(color as Number) as Number {
        return color == Graphics.COLOR_BLUE ? Graphics.COLOR_WHITE : color;
    }

    // [x, width] of a horizontal band at height y on a round screen of width w, with margin m.
    function span(w as Number, y as Number, m as Number) as [Number, Number] {
        var r = w / 2;
        var dy = (y - r).abs();
        var half = dy >= r ? 0 : Math.sqrt(r * r - dy * dy).toNumber();
        return [r - half + m, 2 * (half - m)];
    }
}
