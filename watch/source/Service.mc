import Toybox.Application;
import Toybox.Background;
import Toybox.Lang;
import Toybox.System;

// Background poll every 5 minutes (the Connect IQ minimum). Writes Storage itself where
// the device allows it (newer than Connect IQ 3.1), so the glance sees fresh data; on
// Connect IQ 3.1 (fēnix 5 series) the app stores the response in onBackgroundData.
(:background)
class Service extends System.ServiceDelegate {

    function initialize() {
        ServiceDelegate.initialize();
    }

    function onTemporalEvent() as Void {
        Api.refresh(method(:onResponse));
    }

    function onResponse(code as Number, data as Dictionary or String or Null) as Void {
        var stored = false;
        try {
            stored = Api.acceptAny(code, data);
        } catch (e) {
            // Connect IQ 3.1: no Storage writes in the background — let the app store it.
            var payload = {"code" => code} as Dictionary<String, Application.PropertyValueType>;
            payload.put("data", data as Application.PropertyValueType);
            Background.exit(payload as Application.PersistableType);
            return;
        }
        Background.exit(stored);
    }
}
