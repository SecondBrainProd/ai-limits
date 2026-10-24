import Toybox.Lang;
import Toybox.Test;

(:test)
function testUntilDaysHours(logger as Test.Logger) as Boolean {
    Test.assertEqual(Ui.until(NOW + 36 * 3600 + 5 * 60, NOW), "1d 12h");
    return true;
}

(:test)
function testUntilHoursMinutes(logger as Test.Logger) as Boolean {
    Test.assertEqual(Ui.until(NOW + 75 * 60, NOW), "1h 15m");
    return true;
}

(:test)
function testUntilUnderOneMinute(logger as Test.Logger) as Boolean {
    Test.assertEqual(Ui.until(NOW + 20, NOW), "1m");
    return true;
}

(:test)
function testAgo(logger as Test.Logger) as Boolean {
    Test.assertEqual(Ui.ago(NOW - 30, NOW), "just now");
    Test.assertEqual(Ui.ago(NOW - 5 * 60, NOW), "5m ago");
    Test.assertEqual(Ui.ago(NOW - 3 * 3600, NOW), "3h ago");
    Test.assertEqual(Ui.ago(NOW - 2 * 86400, NOW), "2d ago");
    return true;
}
