import unittest
from types import SimpleNamespace

import app as design


class AboutTest(unittest.TestCase):
    def test_about_shows_fork_maintainer_and_description(self):
        design.Adw.init()
        window = design.Adw.Window()
        self.addCleanup(window.destroy)
        window.present()
        ui = SimpleNamespace(win=window)
        design.Preview.about(ui)
        dialog = ui.about_dialog
        self.addCleanup(dialog.force_close)
        self.assertEqual(dialog.get_developer_name(), 'Maicss')
        self.assertIn('音源插件', dialog.get_comments())
        self.assertIn('Linux', dialog.get_comments())
        name = dialog.get_application_icon()
        self.assertEqual(name, 'tinglan-about-logo')
        theme = design.Gtk.IconTheme.get_for_display(window.get_display())
        theme.add_search_path(str(design.UI_DIR / 'icons'))
        icon = theme.lookup_icon(name, None, 128, 1, design.Gtk.TextDirection.NONE, 0)
        self.assertTrue(icon.get_file().get_path().endswith('/tinglan-about-logo.svg'))
