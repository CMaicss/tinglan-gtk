"""Exercise both live quality menus using real GTK button signals."""
import io
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import app as d
from live import Live


class LiveQualityTest(unittest.TestCase):
    def test_wide_and_compact_entries_send_current_track(self):
        d.Adw.init()
        application = d.Adw.Application(flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        backend = SimpleNamespace(stdin=io.StringIO())
        args = SimpleNamespace(backend=Path('/unused'), width=1140, height=760,
                               page='favorites', queue=False, smoke=False,
                               light=False, dark=False)
        with patch('live.subprocess.Popen', return_value=backend), patch('live.threading.Thread'):
            ui = Live(application, args, d.Preview)
        try:
            ui.state = {'Queue': [{'ID': 'huawei:one'}], 'Current': 0}
            ui.update_quality(dict(ui.state, QualityRequested='lossless', QualityActual='standard', QualitySwitching=True), ui.state)
            self.assertEqual(ui.quality_button.get_tooltip_text(), '当前音质：标准（MP3）')
            ui.quality_button.get_popover().get_child().get_last_child().emit('clicked')
            ui.more.get_popover().get_child().get_first_child().get_next_sibling().emit('clicked')
            import json
            commands = [json.loads(line) for line in backend.stdin.getvalue().splitlines()]
            self.assertEqual(commands[-2:], [
                {'Op': 'quality-switch', 'ID': 'huawei:one', 'Text': 'lossless'},
                {'Op': 'quality-switch', 'ID': 'huawei:one', 'Text': 'standard'},
            ])
        finally:
            ui.quit_window()

    def test_play_mode_controls_follow_backend_and_compact_confirmation(self):
        import json
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestPlayMode', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        backend = SimpleNamespace(stdin=io.StringIO())
        args = SimpleNamespace(backend=Path('/unused'), width=640, height=760,
                               page='favorites', queue=False, smoke=False, light=False, dark=False)
        with patch('live.subprocess.Popen', return_value=backend), patch('live.threading.Thread'):
            ui = Live(application, args, d.Preview)
        self.addCleanup(ui.quit_window)
        def mode_commands():
            return [c['Index'] for c in map(json.loads, backend.stdin.getvalue().splitlines()) if c['Op'] == 'mode']
        icons = ['media-playlist-consecutive-symbolic', 'media-playlist-repeat-symbolic',
                 'media-playlist-repeat-song-symbolic', 'media-playlist-shuffle-symbolic']
        theme = d.Gtk.IconTheme.get_for_display(ui.win.get_display())
        for i, icon in enumerate(icons):
            self.assertTrue(theme.has_icon(icon), icon)
            ui.settings_apply({'Settings': {'Mode': i}})
            self.assertEqual(ui.mode_button.get_icon_name(), icon)
            self.assertEqual(ui.compact_mode_icon.get_icon_name(), icon)
        self.assertEqual(mode_commands(), [])
        ui.mode_button.get_popover().get_child().get_first_child().emit('clicked')
        self.assertEqual(mode_commands(), [0])
        entry = ui.more.get_popover().get_child().get_first_child()
        for response, expected in [('cancel', [0]), ('confirm', [0, 2])]:
            opened = []
            with patch.object(d.Adw.AlertDialog, 'present', lambda dialog, _window: opened.append(dialog)):
                entry.emit('clicked')
            dialog, = opened
            choices = dialog.get_extra_child()
            self.assertEqual(choices.get_selected(), 3)
            choices.set_selected(2)
            self.assertEqual(mode_commands(), [0])
            dialog.emit('response', response)
            self.assertEqual(mode_commands(), expected)

    def test_stage_badge_tracks_actual_codec_without_rebuilding_lyrics(self):
        d.Adw.init()
        application = d.Adw.Application(application_id="org.tinglan.TestQualityBadge", flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        backend = SimpleNamespace(stdin=io.StringIO())
        args = SimpleNamespace(backend=Path('/unused'), width=1140, height=760,
                               page='favorites', queue=False, smoke=False,
                               light=False, dark=False)
        with patch('live.subprocess.Popen', return_value=backend), patch('live.threading.Thread'):
            ui = Live(application, args, d.Preview)
        try:
            d.TRACKS.append(('Fixture song', 'Fixture artist', 'Fixture album', '1:00'))
            ui.current = 0
            ui.state = {'Current': 0, 'QualityRequested': 'lossless', 'QualityActual': 'standard'}
            ui.lyrics()
            badge = ui.stage_flac_badge
            self.assertTrue(badge.get_visible())
            revealer = badge.get_last_child()
            self.assertIsInstance(revealer, d.Gtk.Revealer)
            self.assertEqual(revealer.get_transition_type(), d.Gtk.RevealerTransitionType.CROSSFADE)
            self.assertFalse(revealer.get_reveal_child())
            self.assertEqual(badge.get_prev_sibling().get_text(), 'Fixture album')
            stage = ui.screen.get_child_by_name('lyrics')
            cover_y = {}
            for actual, visible in [('lossless', True), ('standard', False), ('', False)]:
                ui.update_quality(dict(ui.state, QualityActual=actual), ui.state)
                self.assertTrue(badge.get_visible())
                self.assertEqual(revealer.get_reveal_child(), visible)
                self.assertIs(ui.screen.get_child_by_name('lyrics'), stage)
                picture = revealer.get_child()
                self.assertEqual(picture.get_paintable().get_intrinsic_width(), 44)
                for variant, width in [('compact', 160), ('regular', 296), ('large', 424)]:
                    ui.stage_cover.set_visible_child_name(variant)
                    artwork = badge.get_parent()
                    artwork.allocate(width, 640, -1, None)
                    self.assertEqual((badge.get_width(), badge.get_height()), (44, 18))
                    if visible:
                        self.assertEqual((picture.get_width(), picture.get_height()), (44, 18))
                    ok, bounds = ui.stage_cover.compute_bounds(artwork)
                    self.assertTrue(ok)
                    if actual == 'lossless':
                        cover_y[variant] = bounds.get_y()
                    else:
                        self.assertEqual(bounds.get_y(), cover_y[variant])
        finally:
            ui.quit_window()
