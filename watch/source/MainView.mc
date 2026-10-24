import Toybox.Graphics;
import Toybox.Lang;
import Toybox.Time;
import Toybox.Timer;
import Toybox.WatchUi;

// Full screen: the limits like the Limits app, or the pairing screen.
// Asks the relay for fresh data as soon as it is shown.
class MainView extends WatchUi.View {

    private var _loading as Boolean = false;
    private var _lastAuto as Number = 0;
    private var _unpairing as Boolean = false;
    private var _unpairFailed as Boolean = false;
    private var _timer as Timer.Timer?;
    const AUTO_SEC = 30;

    function initialize() {
        View.initialize();
    }

    function onShow() as Void {
        refresh();
        // Redraw every 30 s so an expiring pairing code is replaced while the screen is open.
        _timer = new Timer.Timer();
        (_timer as Timer.Timer).start(method(:tick), AUTO_SEC * 1000, true);
    }

    function onHide() as Void {
        if (_timer != null) {
            (_timer as Timer.Timer).stop();
            _timer = null;
        }
    }

    function tick() as Void {
        WatchUi.requestUpdate();
    }

    function refresh() as Void {
        if (_loading) {
            return;
        }
        _loading = true;
        WatchUi.requestUpdate();
        Api.refresh(method(:onResponse));
    }

    function unpair() as Void {
        _loading = true;
        _unpairing = true;
        Api.unpair(method(:onResponse));
    }

    function onResponse(code as Number, data as Dictionary or String or Null) as Void {
        _loading = false;
        // Unpair without a connection: say so instead of silently showing the old limits.
        _unpairFailed = _unpairing && code != 200;
        _unpairing = false;
        Api.acceptAny(code, data);
        if (Api.retryNow(code)) {
            refresh();
            return;
        }
        WatchUi.requestUpdate();
    }

    function onUpdate(dc as Graphics.Dc) as Void {
        var w = dc.getWidth();
        var h = dc.getHeight();
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_BLACK);
        dc.clear();
        dc.setColor(0xFF5500, Graphics.COLOR_TRANSPARENT);
        dc.drawText(w / 2, h * 9 / 100, Graphics.FONT_XTINY, Ui.unit(Rez.Strings.AppName), Graphics.TEXT_JUSTIFY_CENTER);

        var st = Api.state();
        if (st != null && st["paired"] == false && st["code"] instanceof String) {
            var now = Time.now().value();
            if (Api.codeExpired(st, now)) {
                // Never show a dead code; ask for a new one at most every AUTO_SEC.
                if (!_loading && now - _lastAuto >= AUTO_SEC) {
                    _lastAuto = now;
                    refresh();
                }
                drawMessage(dc, w, h);
                return;
            }
            drawPairing(dc, w, h, st["code"] as String);
            return;
        }
        var snap = st != null && st["snap"] instanceof Dictionary ? st["snap"] as Dictionary : null;
        if (snap == null) {
            drawMessage(dc, w, h);
            return;
        }
        drawLimits(dc, w, h, snap);
    }

    private function drawMessage(dc as Graphics.Dc, w as Number, h as Number) as Void {
        var text = _loading ? Ui.unit(Rez.Strings.Loading) : Ui.unit(Rez.Strings.NoData);
        var e = Api.lastError();
        if (!_loading && e != null && e < 0) {
            text = Ui.unit(Rez.Strings.NoConnection) + " (" + e + ")";
        }
        dc.setColor(Graphics.COLOR_LT_GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(w / 2, h / 2, Graphics.FONT_SMALL, text, Graphics.TEXT_JUSTIFY_CENTER | Graphics.TEXT_JUSTIFY_VCENTER);
    }

    private function drawPairing(dc as Graphics.Dc, w as Number, h as Number, code as String) as Void {
        var fSmall = Graphics.FONT_XTINY;
        var sh = dc.getFontHeight(fSmall);
        var y = h * 9 / 100 + sh + h * 6 / 100;
        dc.setColor(Graphics.COLOR_LT_GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(w / 2, y, fSmall, Ui.unit(Rez.Strings.ForClaudeCode), Graphics.TEXT_JUSTIFY_CENTER);
        y += sh + h * 3 / 100;
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        // FONT_LARGE, not a number font: number fonts lack letters on some devices.
        var f = Graphics.FONT_LARGE;
        dc.drawText(w / 2, y, f, code, Graphics.TEXT_JUSTIFY_CENTER);
        y += dc.getFontHeight(f) + h * 3 / 100;
        dc.setColor(Graphics.COLOR_LT_GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(w / 2, y, fSmall, Ui.unit(Rez.Strings.PairRun), Graphics.TEXT_JUSTIFY_CENTER);
        y += sh;
        dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
        dc.drawText(w / 2, y, Graphics.FONT_TINY, "ailimits pair " + code, Graphics.TEXT_JUSTIFY_CENTER);
        y += dc.getFontHeight(Graphics.FONT_TINY) + h * 2 / 100;
        // Full URL where it fits the circle at this height, the short form otherwise, none on tiny screens.
        var room = Ui.span(w, y + sh, w / 30)[1];
        var link = "github.com/SecondBrainProd/ai-limits";
        if (dc.getTextWidthInPixels(link, fSmall) > room) {
            link = "SecondBrainProd/ai-limits";
        }
        if (dc.getTextWidthInPixels(link, fSmall) <= room) {
            dc.setColor(Graphics.COLOR_DK_GRAY, Graphics.COLOR_TRANSPARENT);
            dc.drawText(w / 2, y, fSmall, link, Graphics.TEXT_JUSTIFY_CENTER);
        }
    }

    private function drawLimits(dc as Graphics.Dc, w as Number, h as Number, snap as Dictionary) as Void {
        var now = Time.now().value();
        var rows = Model.rowsFor(snap, now, false);
        var n = rows.size();
        var fSmall = Graphics.FONT_XTINY;
        var sh = dc.getFontHeight(fSmall);
        var barH = h / 45 < 4 ? 4 : h / 45;
        var areaTop = h * 9 / 100 + sh;
        var areaBottom = h * 91 / 100 - sh;
        // Biggest label font whose blocks still leave a gap; small screens (218–240 px) step down.
        var fLabel = Graphics.FONT_TINY;
        var lh = dc.getFontHeight(fLabel);
        var blockH = lh + barH + 2 + sh;
        var gap = n == 0 ? 0 : (areaBottom - areaTop - n * blockH) / (n + 1);
        if (gap < 3) {
            fLabel = Graphics.FONT_XTINY;
            lh = dc.getFontHeight(fLabel);
            blockH = lh + barH + 2 + sh;
            gap = n == 0 ? 0 : (areaBottom - areaTop - n * blockH) / (n + 1);
        }
        // Still no room (218 px screens): drop the "resets in" lines.
        var showSub = true;
        if (gap < 3) {
            showSub = false;
            blockH = lh + barH + 2;
            gap = n == 0 ? 0 : (areaBottom - areaTop - n * blockH) / (n + 1);
        }
        if (gap < 0) {
            gap = 0;
        }
        var y0 = areaTop + gap;
        // One column for all rows, as wide as the narrowest point of the circle it spans.
        var sTop = Ui.span(w, y0, w / 20);
        var sBot = Ui.span(w, y0 + n * blockH + (n - 1) * gap, w / 20);
        var col = sTop[1] < sBot[1] ? sTop : sBot;
        var x = col[0];
        var bw = col[1];

        for (var i = 0; i < n; i++) {
            var r = rows[i];
            var c = r[:color] as Number;
            var y = y0 + (blockH + gap) * i;
            var pctText = showSub ? (r[:pct] as Number) + "%"
                : Model.compactPct(r[:pct] as Number, r[:reset] as Number, now);
            var label = r[:label] as String;
            var room = bw - dc.getTextWidthInPixels(pctText + "  ", fLabel);
            if (dc.getTextWidthInPixels(label, fLabel) > room) {
                label = Ui.fit(dc, r[:short] as String, fLabel, room);
            }
            dc.setColor(Graphics.COLOR_WHITE, Graphics.COLOR_TRANSPARENT);
            dc.drawText(x, y, fLabel, label, Graphics.TEXT_JUSTIFY_LEFT);
            dc.setColor(Ui.pctColor(c), Graphics.COLOR_TRANSPARENT);
            dc.drawText(x + bw, y, fLabel, pctText, Graphics.TEXT_JUSTIFY_RIGHT);
            Ui.drawBar(dc, x, y + lh, bw, barH, r[:pct] as Number, c);
            var sub = (r[:resetDone] as Boolean) ? Ui.unit(Rez.Strings.ResetDone)
                : ((r[:reset] as Number) > 0 ? Ui.unit(Rez.Strings.ResetsIn) + " " + Ui.until(r[:reset] as Number, now) : "");
            if (showSub) {
                dc.setColor(Graphics.COLOR_LT_GRAY, Graphics.COLOR_TRANSPARENT);
                dc.drawText(x, y + lh + barH + 2, fSmall, sub, Graphics.TEXT_JUSTIFY_LEFT);
            }
        }

        var t = snap["t"] instanceof Number ? snap["t"] as Number : 0;
        var foot = _loading ? Ui.unit(Rez.Strings.Loading) : Ui.unit(Rez.Strings.Updated) + " " + Ui.ago(t, now);
        if (_unpairFailed && !_loading) {
            foot = Ui.unit(Rez.Strings.NoConnection);
        }
        dc.setColor(Model.isStale(t, now) ? Graphics.COLOR_RED : Graphics.COLOR_LT_GRAY, Graphics.COLOR_TRANSPARENT);
        dc.drawText(w / 2, areaBottom, fSmall, foot, Graphics.TEXT_JUSTIFY_CENTER);
    }
}
