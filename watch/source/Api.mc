import Toybox.Application;
import Toybox.Application.Storage;
import Toybox.Communications;
import Toybox.Lang;

// Talks to the AI Limits relay. Shared by the background service, the glance and the main view.
//   POST /v1/watches           → {id, secret, code, code_expires}
//   GET/DELETE /v1/watch       → {paired:false, code, code_expires} | {paired:true, snap}
// The watch secret is kept in Storage and never shown or logged.
(:background :glance)
module Api {
    const BASE = "https://ailimits.second-brain.ru";
    const K_ID = "id";
    const K_SECRET = "secret";
    const K_STATE = "state";
    const K_ERR = "err";

    typedef Callback as Method(code as Number, data as Dictionary or String or Null) as Void;

    function needsRegistration() as Boolean {
        return !(Storage.getValue(K_ID) instanceof String) || !(Storage.getValue(K_SECRET) instanceof String);
    }

    // Registers when needed, otherwise polls. The callback gets the raw response;
    // pass it to acceptAny so both shapes are handled.
    function refresh(cb as Callback) as Void {
        if (needsRegistration()) {
            Communications.makeWebRequest(BASE + "/v1/watches", {}, {
                :method => Communications.HTTP_REQUEST_METHOD_POST,
                :headers => {"Content-Type" => Communications.REQUEST_CONTENT_TYPE_JSON},
                :responseType => Communications.HTTP_RESPONSE_CONTENT_TYPE_JSON
            }, cb);
        } else {
            request(Communications.HTTP_REQUEST_METHOD_GET, cb);
        }
    }

    function unpair(cb as Callback) as Void {
        request(Communications.HTTP_REQUEST_METHOD_DELETE, cb);
    }

    function request(method as Communications.HttpRequestMethod, cb as Callback) as Void {
        var auth = "Bearer " + Storage.getValue(K_ID) + "." + Storage.getValue(K_SECRET);
        Communications.makeWebRequest(BASE + "/v1/watch", null, {
            :method => method,
            :headers => {"Authorization" => auth},
            :responseType => Communications.HTTP_RESPONSE_CONTENT_TYPE_JSON
        }, cb);
    }

    // Routes a response from refresh(): 201 is a registration, everything else a watch state.
    function acceptAny(code as Number, data as Dictionary or String or Null) as Boolean {
        return code == 201 ? acceptRegistration(code, data) : accept(code, data);
    }

    function acceptRegistration(code as Number, data as Dictionary or String or Null) as Boolean {
        if (code == 201 && data instanceof Dictionary && data["id"] instanceof String && data["secret"] instanceof String) {
            Storage.setValue(K_ID, data["id"] as String);
            Storage.setValue(K_SECRET, data["secret"] as String);
            var st = {"paired" => false, "code" => data["code"], "code_expires" => data["code_expires"]};
            Storage.setValue(K_STATE, st as Dictionary<Storage.KeyType, Storage.ValueType>);
            Storage.deleteValue(K_ERR);
            return true;
        }
        Storage.setValue(K_ERR, code);
        return false;
    }

    // 200: new state. 401: the relay forgot this watch (cleanup or data loss) — forget the
    // credentials so the next refresh registers again. Anything else: keep the last state.
    function accept(code as Number, data as Dictionary or String or Null) as Boolean {
        if (code == 200 && data instanceof Dictionary && data["paired"] instanceof Boolean) {
            Storage.setValue(K_STATE, data as Dictionary<Storage.KeyType, Storage.ValueType>);
            Storage.deleteValue(K_ERR);
            return true;
        }
        if (code == 401) {
            Storage.deleteValue(K_ID);
            Storage.deleteValue(K_SECRET);
            Storage.deleteValue(K_STATE);
        }
        Storage.setValue(K_ERR, code);
        return false;
    }

    // Older devices (Connect IQ 3.1) forbid Storage writes in the background process; the
    // service then hands the raw response over via Background.exit() and the app stores it here.
    function acceptBackgroundPayload(payload as Application.PersistableType) as Boolean {
        if (payload instanceof Dictionary && payload["code"] instanceof Number) {
            return acceptAny(payload["code"] as Number, payload["data"] as Dictionary or String or Null);
        }
        return false;
    }

    // Pairing codes live 15 minutes on the relay. An unknown expiry counts as expired so
    // the watch asks for a fresh code rather than showing a dead one.
    function codeExpired(st as Dictionary, now as Number) as Boolean {
        var ce = st["code_expires"];
        if (ce instanceof Number) {
            return ce <= now;
        }
        return true;
    }

    // After a 401 the credentials are gone; the next refresh registers again (one POST).
    function retryNow(code as Number) as Boolean {
        return code == 401;
    }

    function state() as Dictionary? {
        var s = Storage.getValue(K_STATE);
        return s instanceof Dictionary ? s as Dictionary : null;
    }

    function lastError() as Number? {
        var e = Storage.getValue(K_ERR);
        return e instanceof Number ? e as Number : null;
    }
}
