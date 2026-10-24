import Toybox.Application.Storage;
import Toybox.Graphics;
import Toybox.Lang;
import Toybox.Test;

const NOW = 1792668000;

// Fixtures (module, so the compiler does not treat them as tests).
(:test)
module Fx {
    function snapOf(t as Number, rows as Array) as Dictionary {
        return {"v" => 1, "t" => t, "rows" => rows};
    }

    function row(kind as String, pct as Number, reset as Number, sev as Number) as Dictionary {
        return {"kind" => kind, "pct" => pct, "reset" => reset, "sev" => sev};
    }
}

(:test)
function testResetPassed(logger as Test.Logger) as Boolean {
    var rows = Model.rowsFor(Fx.snapOf(NOW, [Fx.row("session", 64, NOW - 1, 0)]), NOW, true);
    Test.assertEqual(rows[0][:pct], 0);
    Test.assertEqual(rows[0][:resetDone], true);
    return true;
}

(:test)
function testResetNotYet(logger as Test.Logger) as Boolean {
    var rows = Model.rowsFor(Fx.snapOf(NOW, [Fx.row("session", 64, NOW + 60, 0)]), NOW, true);
    Test.assertEqual(rows[0][:pct], 64);
    Test.assertEqual(rows[0][:resetDone], false);
    return true;
}

(:test)
function testUnknownKindUsesLabel(logger as Test.Logger) as Boolean {
    var r = Fx.row("weekly_scoped", 99, NOW + 60, 2);
    r["label"] = "Fable";
    var rows = Model.rowsFor(Fx.snapOf(NOW, [r]), NOW, true);
    Test.assertEqual(rows[0][:label], "Fable");
    return true;
}

(:test)
function testLabelsEng(logger as Test.Logger) as Boolean {
    Test.assertEqual(Model.labelFor("session", null, true), "5h");
    Test.assertEqual(Model.labelFor("session", null, false), "Session · 5h");
    Test.assertEqual(Model.labelFor("weekly_all", null, true), "7d");
    Test.assertEqual(Model.labelFor("weekly_all", null, false), "Week");
    Test.assertEqual(Model.labelFor("weird", null, true), "weird");
    return true;
}

(:test)
function testColorThresholds(logger as Test.Logger) as Boolean {
    Test.assertEqual(Model.colorFor(74, 0, false), Graphics.COLOR_BLUE);
    Test.assertEqual(Model.colorFor(75, 0, false), Graphics.COLOR_ORANGE);
    Test.assertEqual(Model.colorFor(89, 0, false), Graphics.COLOR_ORANGE);
    Test.assertEqual(Model.colorFor(90, 0, false), Graphics.COLOR_RED);
    Test.assertEqual(Model.colorFor(10, 1, false), Graphics.COLOR_ORANGE);
    Test.assertEqual(Model.colorFor(10, 2, false), Graphics.COLOR_RED);
    Test.assertEqual(Model.colorFor(99, 2, true), Graphics.COLOR_LT_GRAY);
    return true;
}

(:test)
function testStale(logger as Test.Logger) as Boolean {
    Test.assertEqual(Model.isStale(NOW - 899, NOW), false);
    Test.assertEqual(Model.isStale(NOW - 901, NOW), true);
    return true;
}

(:test)
function testMaxThreeRows(logger as Test.Logger) as Boolean {
    var rs = [Fx.row("session", 1, 0, 0), Fx.row("weekly_all", 2, 0, 0), Fx.row("a", 3, 0, 0), Fx.row("b", 4, 0, 0)];
    Test.assertEqual(Model.rowsFor(Fx.snapOf(NOW, rs), NOW, true).size(), 3);
    return true;
}

(:test)
function testAcceptKeepsSnapOnError(logger as Test.Logger) as Boolean {
    Storage.clearValues();
    var paired = {"paired" => true, "snap" => Fx.snapOf(NOW, [Fx.row("session", 7, 0, 0)])};
    Api.accept(200, paired);
    Api.accept(-104, null);
    var st = Api.state();
    Test.assertEqual(st["paired"], true);
    Test.assertEqual(Api.lastError(), -104);
    return true;
}

(:test)
function testAcceptNewCode(logger as Test.Logger) as Boolean {
    Storage.clearValues();
    Api.accept(200, {"paired" => false, "code" => "AAAAAA", "code_expires" => NOW});
    Api.accept(200, {"paired" => false, "code" => "BBBBBB", "code_expires" => NOW + 900});
    Test.assertEqual(Api.state()["code"], "BBBBBB");
    Test.assert(Api.lastError() == null);
    return true;
}

(:test)
function testUnauthorizedReRegisters(logger as Test.Logger) as Boolean {
    Storage.clearValues();
    Api.acceptRegistration(201, {"id" => "0123456789abcdef0123456789abcdef", "secret" => "s", "code" => "CCCCCC", "code_expires" => NOW});
    Test.assertEqual(Api.needsRegistration(), false);
    Api.accept(401, null);
    Test.assertEqual(Api.needsRegistration(), true);
    Test.assert(Api.state() == null);
    return true;
}

(:test)
function testRowsHaveShortLabel(logger as Test.Logger) as Boolean {
    var rows = Model.rowsFor(Fx.snapOf(NOW, [Fx.row("session", 8, NOW + 60, 0)]), NOW, false);
    Test.assertEqual(rows[0][:label], "Session · 5h");
    Test.assertEqual(rows[0][:short], "5h");
    return true;
}

(:test)
function testBackgroundPayloadAccepted(logger as Test.Logger) as Boolean {
    Storage.clearValues();
    var ok = Api.acceptBackgroundPayload({"code" => 200, "data" => {"paired" => false, "code" => "QQQQQQ", "code_expires" => NOW}});
    Test.assertEqual(ok, true);
    Test.assertEqual(Api.state()["code"], "QQQQQQ");
    Test.assertEqual(Api.acceptBackgroundPayload(true), false);
    return true;
}

(:test)
function testCodeExpired(logger as Test.Logger) as Boolean {
    Test.assertEqual(Api.codeExpired({"paired" => false, "code" => "AAAAAA", "code_expires" => NOW - 1}, NOW), true);
    Test.assertEqual(Api.codeExpired({"paired" => false, "code" => "AAAAAA", "code_expires" => NOW + 60}, NOW), false);
    // no expiry known → treat as expired so the watch asks the relay for a fresh code
    Test.assertEqual(Api.codeExpired({"paired" => false, "code" => "AAAAAA"}, NOW), true);
    return true;
}

(:test)
function testRetryAfterUnauthorized(logger as Test.Logger) as Boolean {
    Test.assertEqual(Api.retryNow(401), true);
    Test.assertEqual(Api.retryNow(200), false);
    Test.assertEqual(Api.retryNow(-104), false);
    return true;
}

(:test)
function testAcceptAnyRoutes201(logger as Test.Logger) as Boolean {
    Storage.clearValues();
    Api.acceptAny(201, {"id" => "0123456789abcdef0123456789abcdef", "secret" => "s", "code" => "RRRRRR", "code_expires" => NOW});
    Test.assertEqual(Api.needsRegistration(), false);
    Api.acceptAny(200, {"paired" => true, "snap" => Fx.snapOf(NOW, [Fx.row("session", 3, 0, 0)])});
    Test.assertEqual(Api.state()["paired"], true);
    return true;
}

(:test)
function testMalformedRegistration(logger as Test.Logger) as Boolean {
    Storage.clearValues();
    Test.assertEqual(Api.acceptAny(201, {"id" => "0123456789abcdef0123456789abcdef"}), false);
    Test.assertEqual(Api.needsRegistration(), true);
    Test.assertEqual(Api.lastError(), 201);
    return true;
}

(:test)
function testRowsSkipNonDictionary(logger as Test.Logger) as Boolean {
    var rows = Model.rowsFor(Fx.snapOf(NOW, [5, Fx.row("session", 7, 0, 0)]), NOW, true);
    Test.assertEqual(rows.size(), 1);
    Test.assertEqual(rows[0][:pct], 7);
    return true;
}

(:test)
function testCompactPctWithReset(logger as Test.Logger) as Boolean {
    Test.assertEqual(Model.compactPct(9, NOW + 7200, NOW), "9% · 2h");
    Test.assertEqual(Model.compactPct(9, NOW - 1, NOW), "0%");
    Test.assertEqual(Model.compactPct(9, 0, NOW), "9%");
    return true;
}
