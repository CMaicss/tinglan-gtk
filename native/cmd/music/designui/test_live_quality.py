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
            self.assertFalse(badge.get_visible())
            self.assertEqual(badge.get_prev_sibling().get_text(), 'Fixture album')
            stage = ui.screen.get_child_by_name('lyrics')
            for actual, visible in [('lossless', True), ('standard', False), ('', False)]:
                ui.update_quality(dict(ui.state, QualityActual=actual), ui.state)
                self.assertEqual(badge.get_visible(), visible)
                self.assertIs(ui.screen.get_child_by_name('lyrics'), stage)
                if visible:
                    picture = badge.get_last_child()
                    self.assertEqual(picture.get_paintable().get_intrinsic_width(), 44)
                    for variant, width in [('compact', 160), ('regular', 296), ('large', 424)]:
                        ui.stage_cover.set_visible_child_name(variant)
                        badge.get_parent().allocate(width, 640, -1, None)
                        self.assertEqual((badge.get_width(), badge.get_height()), (44, 18))
                        self.assertEqual((picture.get_width(), picture.get_height()), (44, 18))
        finally:
            ui.quit_window()
