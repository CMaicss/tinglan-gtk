import io
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import app as d
from live import Live
from test_live_art_delivery import png_fixture


class StageBackdropTest(unittest.TestCase):
    def test_live_stage_ignores_late_reply_after_rapid_skip(self):
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestLateArt', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        args = SimpleNamespace(backend=Path('/unused'), width=1140, height=760,
                               page='favorites', queue=False, smoke=False, light=False, dark=False)
        with patch('live.subprocess.Popen', return_value=SimpleNamespace(stdin=io.StringIO())), patch('live.threading.Thread'):
            ui = Live(application, args, d.Preview)
        self.addCleanup(ui.quit_window)
        ui.art_apply({'Art': {'Epoch': 1}})
        queue = [{'ID': f's:{n}'} for n in range(3)]

        def reply(current, delivered, width):
            ui.current = current
            ui.state = {'Queue': queue, 'Current': current, 'Art': {'Epoch': 1, 'Reply': {
                'ID': queue[delivered]['ID'], 'Field': 'Stage', 'Index': delivered, 'PNG': png_fixture(width)}}}
            ui._art_request = ('Stage', delivered, queue[delivered]['ID'])
            ui.art_apply(ui.state)
            ui.refresh_stage_backdrop()

        ui.view = 'lyrics'
        reply(0, 0, 12)
        self.assertEqual(ui.stage_backdrop.texture.get_width(), 12)
        reply(2, 1, 24)
        self.assertIsNone(ui.stage_background_texture())
        reply(2, 2, 40)
        current = ui.stage_backdrop.texture
        self.assertEqual(current.get_width(), 40)
        self.assertFalse(getattr(ui, '_stage_grace_source', 0))
        reply(2, 0, 64)
        self.assertIs(ui.stage_backdrop.texture, current)
        ui.open('favorites')

    def test_cover_geometry_and_page_scope(self):
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestBackdrop', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        args = SimpleNamespace(width=1140, height=760, page='favorites', queue=False, smoke=False, light=False, dark=False)
        tracks = [('潮汐来信', '迟岛', '海岸线', '4:26')] * 6
        with patch.object(d, 'TRACKS', tracks):
            ui = d.Preview(application, args)
            self.addCleanup(ui.win.destroy)
            ui.lyrics()
            self.assertTrue(ui.stage_backdrop.get_visible())
            self.assertIsNotNone(ui.stage_backdrop.texture)
            overlay = ui.stage_backdrop.get_parent()
            self.assertIs(overlay.get_child(), ui.main_pane)
            self.assertIs(ui.header.get_parent(), ui.main_pane)
            self.assertIs(ui.player_inner.get_parent().get_parent(), ui.main_pane)
            rect = ui.stage_backdrop.cover_rect(1000, 600, 400, 200)
            self.assertAlmostEqual(rect[2] / rect[3], 2)
            self.assertLessEqual(rect[0], 0)
            self.assertLessEqual(rect[1], 0)
            self.assertGreaterEqual(rect[0] + rect[2], 1000)
            self.assertGreaterEqual(rect[1] + rect[3], 600)
            old = ui.stage_backdrop.texture
            face = ui.stage_cover.get_visible_child().get_last_child()
            self.assertIs(face.transition, ui.stage_backdrop)
            with patch.object(ui, 'stage_background_texture', return_value=None):
                ui.refresh_stage_backdrop()
                self.assertIs(ui.stage_backdrop.texture, old)
            ui.current = 1
            ui.lyrics()
            self.assertIsNot(ui.stage_backdrop.texture, old)
            self.assertIs(ui.stage_cover.get_visible_child().get_last_child().transition,
                          ui.stage_backdrop)
            # Rapidly skip to a track with no image, and keep polling it.
            outgoing = ui.stage_backdrop.texture
            with patch.object(ui, 'stage_background_texture', return_value=None):
                ui.current = 2
                ui.lyrics()
                self.assertIs(ui.stage_backdrop.texture, outgoing)
                loop = d.GLib.MainLoop()
                poll = d.GLib.timeout_add(25, lambda: (ui.refresh_stage_backdrop(), True)[1])
                def finish():
                    loop.quit()
                    return False
                d.GLib.timeout_add(650, finish)
                try:
                    loop.run()
                finally:
                    d.GLib.source_remove(poll)
                self.assertIsNone(ui.stage_backdrop.texture)
                self.assertIsNone(ui.stage_backdrop.outgoing)
                self.assertFalse(ui.stage_backdrop.get_visible())
            ui.refresh_stage_backdrop()
            self.assertIs(ui.stage_backdrop.texture, ui.stage_background_texture())
            ui.open('favorites')
            self.assertFalse(ui.stage_backdrop.get_visible())
            self.assertIsNone(ui.stage_backdrop.texture)
