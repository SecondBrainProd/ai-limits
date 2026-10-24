import Toybox.Lang;
import Toybox.WatchUi;

// Start: refresh now. Menu (long Up): confirm, then unpair this watch.
class Delegate extends WatchUi.BehaviorDelegate {

    private var _view as MainView;

    function initialize(view as MainView) {
        BehaviorDelegate.initialize();
        _view = view;
    }

    function onSelect() as Boolean {
        _view.refresh();
        return true;
    }

    function onMenu() as Boolean {
        WatchUi.pushView(new WatchUi.Confirmation(Ui.unit(Rez.Strings.UnpairQ)),
                         new UnpairConfirm(_view), WatchUi.SLIDE_IMMEDIATE);
        return true;
    }
}

class UnpairConfirm extends WatchUi.ConfirmationDelegate {

    private var _view as MainView;

    function initialize(view as MainView) {
        ConfirmationDelegate.initialize();
        _view = view;
    }

    function onResponse(response as WatchUi.Confirm) as Boolean {
        if (response == WatchUi.CONFIRM_YES) {
            _view.unpair();
        }
        return true;
    }
}
