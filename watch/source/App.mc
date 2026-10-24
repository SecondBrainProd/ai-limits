import Toybox.Application;
import Toybox.Background;
import Toybox.Lang;
import Toybox.System;
import Toybox.Time;
import Toybox.WatchUi;

// AI Limits — Claude Code usage limits in the widget loop.
(:background :glance)
class App extends Application.AppBase {
    const POLL_SEC = 300;

    function initialize() {
        AppBase.initialize();
    }

    (:glance)
    function registerBackground() as Void {
        // Register once: re-registering on every open could restart the countdown
        // on some firmware and delay polls for someone who scrolls glances often.
        if ((Toybox.System has :ServiceDelegate) && Background.getTemporalEventRegisteredTime() == null) {
            Background.registerForTemporalEvent(new Time.Duration(POLL_SEC));
        }
    }

    (:typecheck([disableBackgroundCheck, disableGlanceCheck]))
    function getInitialView() as [WatchUi.Views] or [WatchUi.Views, WatchUi.InputDelegates] {
        registerBackground();
        var view = new MainView();
        return [view, new Delegate(view)];
    }

    (:glance :typecheck(disableBackgroundCheck))
    function getGlanceView() as [WatchUi.GlanceView] or [WatchUi.GlanceView, WatchUi.GlanceViewDelegate] or Null {
        registerBackground();
        return [new LimitsGlance()];
    }

    (:typecheck(disableGlanceCheck))
    function getServiceDelegate() as [System.ServiceDelegate] {
        return [new Service()];
    }

    (:typecheck(disableBackgroundCheck))
    function onBackgroundData(data as Application.PersistableType) as Void {
        Api.acceptBackgroundPayload(data);
        WatchUi.requestUpdate();
    }
}
