import io
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import app as d
from live import Live
from test_live_art_delivery import png_fixture


class StageBackdropTest(unittest.TestCase):
    def test_titlebar_button_keeps_maximize_separate_from_fullscreen(self):
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestWindowModes', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        args = SimpleNamespace(width=960, height=640, page='favorites', queue=False, smoke=False, light=False, dark=False)
        with patch.object(d, 'TRACKS', [('歌曲', '歌手', '专辑', '1:00')] * 6):
            ui = d.Preview(application, args)
            self.addCleanup(ui.win.destroy)
        self.assertEqual(ui.window_size_button.get_icon_name(), 'window-maximize-symbolic')
        for fullscreen, maximized, calls in (
                (False, False, ['maximize']), (False, True, ['unmaximize']),
                (True, False, ['unfullscreen']), (True, True, ['unfullscreen', 'unmaximize'])):
            actions = []
            fake = SimpleNamespace(is_fullscreen=lambda: fullscreen, is_maximized=lambda: maximized)
            for action in ('maximize', 'unmaximize', 'unfullscreen'):
                setattr(fake, action, lambda name=action: actions.append(name))
            with patch.object(ui, 'win', fake):
                ui.sync_window_size_button()
                expected = 'window-restore-symbolic' if fullscreen or maximized else 'window-maximize-symbolic'
                self.assertEqual(ui.window_size_button.get_icon_name(), expected)
                ui.window_size_button.emit('clicked')
                self.assertEqual(actions, calls)

    def test_playback_chrome_hides_after_idle_and_returns_on_motion(self):
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestStageChrome', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        args = SimpleNamespace(width=640, height=760, page='favorites', queue=False, smoke=False, light=False, dark=False)
        with patch.object(d, 'TRACKS', [('歌曲', '歌手', '专辑', '1:00')] * 6):
            ui = d.Preview(application, args)
            self.addCleanup(ui.win.destroy)
            ui.chrome_idle_ms = 80
            settings = ui.win.get_settings()
            animations = settings.get_property('gtk-enable-animations')
            self.addCleanup(settings.set_property, 'gtk-enable-animations', animations)
            # This state test also runs alongside windows that may throttle frame delivery.
            settings.set_property('gtk-enable-animations', False)
            ui.win.present()
            ui.lyrics()
            self.assertFalse(ui.compact_brand.get_visible())
            self.assertTrue(ui.header_revealer.get_reveal_child())
            loop = d.GLib.MainLoop()
            def finish():
                loop.quit()
                return False
            deadline = d.GLib.get_monotonic_time() + 2_000_000
            def wait_for_hidden():
                hidden = (not ui.header_revealer.get_reveal_child()
                          and ui.header_revealer.get_height() == ui.player_revealer.get_height() == 0)
                return finish() if hidden or d.GLib.get_monotonic_time() >= deadline else True
            d.GLib.timeout_add(16, wait_for_hidden)
            loop.run()
            self.assertFalse(ui.header_revealer.get_child_revealed())
            self.assertFalse(ui.player_revealer.get_child_revealed())
            self.assertEqual(ui.header_revealer.get_height(), 0)
            self.assertEqual(ui.player_revealer.get_height(), 0)
            ui.chrome_motion.emit('motion', 200.0, 300.0)
            self.assertTrue(ui.header_revealer.get_reveal_child())
            self.assertTrue(ui.player_revealer.get_reveal_child())
            self.assertFalse(ui.compact_brand.get_visible())
            ui.open('favorites')
            self.assertTrue(ui.compact_brand.get_visible())
            d.GLib.timeout_add(120, finish)
            loop.run()
            self.assertTrue(ui.header_revealer.get_reveal_child())
            self.assertTrue(ui.player_revealer.get_reveal_child())

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

    def test_hd_upgrade_paints_without_waiting_for_animation_tick(self):
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestHDRedraw', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        args = SimpleNamespace(backend=Path('/unused'), width=1140, height=760,
                               page='favorites', queue=False, smoke=False, light=False, dark=False)
        with patch('live.subprocess.Popen', return_value=SimpleNamespace(stdin=io.StringIO())), patch('live.threading.Thread'):
            ui = Live(application, args, d.Preview)
        self.addCleanup(ui.quit_window)
        bg = ui.stage_backdrop
        settings = ui.win.get_settings()
        self.addCleanup(settings.set_property, 'gtk-enable-animations', settings.get_property('gtk-enable-animations'))
        settings.set_property('gtk-enable-animations', True)
        ui.win.present()
        ui.view, ui.current = 'lyrics', 0
        state = ui.state = {'Queue': [{'ID': 's:one'}], 'Current': 0,
                            'Art': {'Epoch': 1, 'Reply': {'ID': 's:one', 'Field': 'Stage', 'Index': 0, 'PNG': png_fixture(192)}}}
        ui.art_apply({'Art': {'Epoch': 1}})
        ui._art_request = ('Stage', 0, 's:one')
        ui.art_apply(state)
        ui.refresh_stage_backdrop()
        low = bg.texture
        self.assertEqual(low.get_width(), 192)
        bg.set_texture(low, animate=False)
        self.assertTrue(bg.get_mapped())

        state['Art'] = {'Epoch': 1, 'StageUpgrade': {'ID': 's:one', 'PNG': png_fixture(640)}}
        with patch.object(d.Adw.TimedAnimation, 'play'), patch.object(bg, 'queue_draw', wraps=bg.queue_draw) as repaint:
            ui.art_apply(state)
            ui.refresh_stage_backdrop()
        self.assertTrue(repaint.called)
        self.assertIsNone(bg.outgoing, 'the low-res cover must not wait for a frame-clock tick to disappear')
        rendered = []
        bg.paint_pair(d.Gtk.Snapshot.new(), lambda tex: rendered.append(tex.get_width()))
        self.assertEqual(rendered, [640])
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
            self.assertTrue(ui.header.is_ancestor(ui.main_pane))
            self.assertTrue(ui.player_inner.is_ancestor(ui.main_pane))
            rect = ui.stage_backdrop.cover_rect(1000, 600, 400, 200)
            self.assertAlmostEqual(rect[2] / rect[3], 2)
            self.assertLessEqual(rect[0], 0)
            self.assertLessEqual(rect[1], 0)
            self.assertGreaterEqual(rect[0] + rect[2], 1000)
            self.assertGreaterEqual(rect[1] + rect[3], 600)
            old = ui.stage_backdrop.texture
            cover = ui.stage_cover.get_visible_child()
            self.assertIsInstance(cover, d.Gtk.Image)
            self.assertEqual(cover.get_size_request(), (296, 296))
            with patch.object(ui, 'stage_background_texture', return_value=None):
                ui.refresh_stage_backdrop()
                self.assertIs(ui.stage_backdrop.texture, old)
            ui.current = 1
            ui.lyrics()
            self.assertIsNot(ui.stage_backdrop.texture, old)
            next_cover = ui.stage_cover.get_visible_child()
            self.assertIsInstance(next_cover, d.Gtk.Image)
            self.assertIsNot(next_cover.get_paintable(), cover.get_paintable())
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
                self.assertTrue(ui.stage_backdrop.get_visible(), 'missing art must keep the dark backdrop')
                self.assertTrue(ui.stage_surface.has_css_class('stage-glass'))
            ui.refresh_stage_backdrop()
            self.assertIs(ui.stage_backdrop.texture, ui.stage_background_texture())
            ui.open('favorites')
            self.assertFalse(ui.stage_backdrop.get_visible())
            self.assertIsNone(ui.stage_backdrop.texture)
