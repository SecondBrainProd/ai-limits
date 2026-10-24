import Toybox.Graphics;
import Toybox.Lang;
import Toybox.Time;
import Toybox.WatchUi;

// Glance: one column per limit — short label / big percent / bar.
(:glance)
class LimitsGlance extends WatchUi.GlanceView {

    function initialize() {
        GlanceView.initialize();
    }

    function onUpdate(dc as Graphics.Dc) as Void {
        var w = dc.getWidth();
        var h = dc.getHeight();
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_BLACK);
        dc.clear();

        var st = Api.state();
        var snap = st != null && st["snap"] instanceof Dictionary ? st["snap"] as Dictionary : null;
        if (snap == null) {
            var msg = Ui.unit(Rez.Strings.AppName) + " · ";
            if (st != null && st["paired"] == false && st["code"] instanceof String) {
                msg += Api.codeExpired(st, Time.now().value())
                    ? Ui.unit(Rez.Strings.OpenToPair)
                    : Ui.unit(Rez.Strings.PairShort) + " " + (st["code"] as String);
            } else {
                msg += Ui.unit(Rez.Strings.NoData);
            }
            dc.drawText(0, h / 2, Graphics.FONT_XTINY, msg, Graphics.TEXT_JUSTIFY_LEFT | Graphics.TEXT_JUSTIFY_VCENTER);
            return;
        }

        var rows = Model.rowsFor(snap, Time.now().value(), true);
        var n = rows.size();
        if (n == 0) {
            return;
        }
        var fLabel = Graphics.FONT_XTINY;
        // The right edge of the glance area runs under the round bezel — keep to the left 82 %.
        var colW = w * 82 / 100 / n;
        // "100%" must fit a column with some air; otherwise step the percent font down.
        var fPct = Graphics.FONT_SMALL;
        if (dc.getTextWidthInPixels("100%", fPct) > colW - colW / 8) {
            fPct = Graphics.FONT_TINY;
        }
        if (dc.getTextWidthInPixels("100%", fPct) > colW - colW / 8) {
            fPct = Graphics.FONT_XTINY;
        }
        var lh = dc.getFontHeight(fLabel);
        var ph = dc.getFontHeight(fPct);
        var top = (h - lh - ph - 5) / 2;
        for (var i = 0; i < n; i++) {
            var r = rows[i];
            var c = r[:color] as Number;
            var x = colW * i + colW / 2;
            dc.setColor(Graphics.COLOR_LT_GRAY, Graphics.COLOR_TRANSPARENT);
            dc.drawText(x, top, fLabel, Ui.fit(dc, r[:label] as String, fLabel, colW - 4), Graphics.TEXT_JUSTIFY_CENTER);
            dc.setColor(Ui.pctColor(c), Graphics.COLOR_TRANSPARENT);
            dc.drawText(x, top + lh - 2, fPct, (r[:pct] as Number) + "%", Graphics.TEXT_JUSTIFY_CENTER);
            Ui.drawBar(dc, x - (colW - 8) / 2, top + lh + ph, colW - 8, 4, r[:pct] as Number, c);
        }
    }
}
