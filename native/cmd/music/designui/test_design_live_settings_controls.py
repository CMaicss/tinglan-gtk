"""Exercise the live settings widgets on an isolated GTK display."""
import unittest
from types import SimpleNamespace

import app as d
from live_settings import settings_mixin


class SettingsHarness(settings_mixin(d)):
    def __init__(self):
        self.win = d.Adw.Window()
        self.args = SimpleNamespace(light=False, dark=False)
        self.sent = []
        self.win.set_content(self.settings_build())

    def page(self, _):
        area = d.box(True)
        return area, area, None

    def send(self, op, **args):
        self.sent.append((op, args))


class DesignLiveSettingsControls(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        d.Adw.init()

    def setUp(self):
        self.ui = SettingsHarness()
        self.state = {'SourceReady': True, 'StoreReady': True, 'FontSize': 100,
                      'Sources': [{'ID': 'fixture', 'Name': 'Fixture', 'External': True}]}
        self.ui.settings_apply({'Settings': self.state})

    def test_only_system_light_dark_without_custom_accent(self):
        model = self.ui.settings_controls['theme'].get_model()
        self.assertEqual([model.get_string(i) for i in range(model.get_n_items())], ['跟随系统', '亮色', '暗色'])
        manager = d.Adw.StyleManager.get_default()
        for index, scheme in enumerate((d.Adw.ColorScheme.DEFAULT, d.Adw.ColorScheme.FORCE_LIGHT, d.Adw.ColorScheme.FORCE_DARK)):
            self.ui.settings_apply({'Settings': dict(self.state, Theme=index, Accent='#ff0000')})
            self.assertEqual(manager.get_color_scheme(), scheme)
            self.assertNotIn('define-color', self.ui.settings_css.to_string())

    def test_snapshot_updates_widgets_without_sending_commands(self):
        self.ui.settings_apply({'Settings': dict(self.state, Lossless=True, Mode=2)})
        self.assertEqual(self.ui.settings_controls['quality'].get_selected(), 1)
        self.assertEqual(self.ui.settings_controls['mode'].get_selected(), 2)
        self.assertEqual(self.ui.sent, [])
        self.ui.settings_controls['quality'].set_selected(0)
        self.assertEqual(self.ui.sent, [('settings-quality', {'Index': 0})])

    def test_external_source_does_not_enable_before_confirmation(self):
        callbacks = []
        self.ui.settings_confirm = lambda title, text, action, confirm, **kw: callbacks.append(confirm)
        row, switch = self.ui.settings_rows['fixture']
        switch.set_active(True)
        self.assertEqual(self.ui.sent, [])
        self.assertFalse(switch.get_active())
        callbacks.pop()()
        self.assertEqual(self.ui.sent, [('settings-source-enable', {'ID': 'fixture', 'Index': 1})])
        self.ui.settings_apply({'Settings': self.state})
        self.assertIs(self.ui.settings_rows['fixture'][0], row)

    def test_polling_preserves_unsaved_jev_fields(self):
        self.ui.settings_jev_fields['Endpoint'].set_text('https://unsaved.example')
        self.ui.settings_apply({'Settings': dict(self.state, Status='poll completed')})
        self.assertEqual(self.ui.settings_jev_fields['Endpoint'].get_text(), 'https://unsaved.example')


if __name__ == '__main__':
    unittest.main()
