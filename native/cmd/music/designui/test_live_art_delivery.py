import base64
import io
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import app as d
from live import Live

from live_art import art_mixin

import gi
gi.require_version('Gtk', '4.0')
gi.require_version('Adw', '1')
from gi.repository import Gtk, Gdk, GLib


def png_fixture(side=1):
    pixel = GLib.Bytes.new(bytes((255, 0, 0, 255)) * side * side)
    texture = Gdk.MemoryTexture.new(side, side, Gdk.MemoryFormat.R8G8B8A8, pixel, 4 * side)
    return base64.b64encode(texture.save_to_png_bytes().get_data()).decode('ascii')


def flush():
    context = GLib.MainContext.default()
    while context.pending():
        context.iteration(False)


class DesignArtDeliveryTest(unittest.TestCase):
    def test_stage_lyric_subtitles_are_smaller_than_original(self):
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestLyricStyle', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        args = SimpleNamespace(backend=Path('/unused'), width=1140, height=760,
                               page='favorites', queue=False, smoke=False, light=False, dark=False)
        backend = SimpleNamespace(stdin=io.StringIO())
        with patch('live.subprocess.Popen', return_value=backend), patch('live.threading.Thread'):
            ui = Live(application, args, d.Preview)
        self.addCleanup(ui.quit_window)
        with patch.object(d, 'TRACKS', [('歌曲', '歌手', '专辑', '1:00')]):
            ui.current = 0
            ui.state = {'Position': 1, 'Lyrics': [{'time': 1, 'text': '原文 & <歌>',
                        'translation': '译文', 'romanization': 'yuen'}, {'time': 2, 'text': '纯正文'}]}
            ui.lyrics()
        self.assertEqual(ui.stage_scroll.get_policy().vscrollbar_policy, Gtk.PolicyType.EXTERNAL)
        row, plain = ui.stage_lines
        original = '原文 & <歌>\n'
        self.assertEqual(row.get_text(), original + '译文\nyuen')
        self.assertTrue(row.has_css_class('active'))
        attrs = row.get_attributes()
        self.assertIsNotNone(attrs, 'subtitles should have their own font scale')
        scales = [a for a in attrs.get_attributes() if a.klass.type == d.Pango.AttrType.SCALE]
        self.assertEqual(len(scales), 1)
        self.assertEqual(scales[0].start_index, len(original.encode('utf-8')))
        self.assertEqual(scales[0].end_index, len(row.get_text().encode('utf-8')))
        self.assertLess(scales[0].as_float().value, 1)
        self.assertIsNone(plain.get_attributes())

    def make_ui(self):
        design = SimpleNamespace(Gtk=Gtk, icon=lambda name, size: Gtk.Image.new_from_icon_name(name))
        ui = art_mixin(design)()
        ui.win = Gtk.Window(default_width=300, default_height=160)
        self.addCleanup(ui.win.destroy)
        ui.cover_btn = Gtk.Button()
        ui.current = None
        ui.indexes = {'Favorites': [4, 5]}
        ui.sent = ui.received = 0
        sent = []
        ui.send = lambda op, **args: sent.append((op, args))
        return ui, sent

    def test_cover_keeps_square_allocation_in_tall_row(self):
        ui, _ = self.make_ui()
        row = Gtk.Box(height_request=100)
        art = ui.art_build(None, 40)
        row.append(art)
        ui.win.set_child(row)
        ui.win.present()
        flush()
        self.assertEqual((art.get_width(), art.get_height()), (40, 40))

    def test_texture_shared_only_by_matching_mapped_widgets(self):
        ui, sent = self.make_ui()
        area = Gtk.Box()
        ui.win.set_child(area)
        art = ui.art_build(4, 40)
        other = ui.art_build(5, 40)
        duplicate = ui.art_build(4, 35)
        for widget in (art, duplicate, other):
            area.append(widget)
        ui.win.present()
        flush()
        state = {'Favorites': [{'ID':'s:one'}, {'ID':'s:two'}], 'Art': {'Epoch':1}}
        ui.art_apply(state)
        self.assertEqual(sent, [('art', {'ID':'s:one', 'Text':'Favorites', 'Index':0})])
        state['Art']['Reply'] = {'ID':'s:one', 'Field':'Favorites', 'Index':0, 'PNG':png_fixture()}
        ui.art_apply(state)
        texture = art.get_last_child().get_paintable()
        self.assertIsInstance(texture, Gdk.Texture)
        self.assertIs(duplicate.get_last_child().get_paintable(), texture)
        self.assertIsNone(other.get_last_child().get_paintable())
        ui.state = state
        before = list(sent)
        art.hide()
        art.show()
        flush()
        self.assertIs(art.get_last_child().get_paintable(), texture)
        self.assertEqual(sent, before)
        state['Art'] = {'Epoch': 1, 'CacheRevision': 1}
        ui.art_apply(state)
        self.assertIsNone(art.get_last_child().get_paintable())


    def test_stage_upgrades_existing_thumb_without_replacing_list_texture(self):
        ui, sent = self.make_ui()
        ui.current = 4
        ui.indexes['Queue'] = [4]
        state = {'Current': 0, 'Queue': [{'ID': 's:one'}], 'Art': {'Epoch': 1}}
        row = Gtk.Box()
        thumb = ui.art_build(4, 40)
        row.append(thumb)
        ui.win.set_child(row)
        ui.win.present()
        flush()
        ui.art_apply(state)
        state['Art']['Reply'] = {'ID': 's:one', 'Field': 'Queue', 'Index': 0, 'PNG': png_fixture(12)}
        ui.art_apply(state)
        stage = ui.art_build(('Stage', 4), 136)
        row.append(stage)
        flush()
        ui.art_apply(state)
        self.assertEqual(stage.get_last_child().get_paintable().get_width(), 12)
        self.assertEqual(sent[-1], ('art', {'ID': 's:one', 'Text': 'Stage', 'Index': 0}))
        state['Art']['Reply'] = {'ID': 's:one', 'Field': 'Stage', 'Index': 0, 'PNG': png_fixture(180)}
        ui.art_apply(state)
        self.assertEqual(stage.get_last_child().get_paintable().get_width(), 180)
        attempts = len(sent)
        state['Art'].pop('Reply')
        ui.art_apply(state)
        self.assertEqual(len(sent), attempts)
        key = ui._art_key(('Stage', 0, 's:one'))
        texture, _ = ui._art_textures[key]
        ui._art_textures[key] = (texture, -1000)
        ui.art_apply(state)
        self.assertEqual(len(sent), attempts + 1)
        self.assertIs(stage.get_last_child().get_paintable(), texture)
        state['Art']['Reply'] = {'ID': 's:one', 'Field': 'Stage', 'Index': 0, 'PNG': png_fixture(640)}
        ui.art_apply(state)
        self.assertEqual(stage.get_last_child().get_paintable().get_width(), 640)
        self.assertEqual(thumb.get_last_child().get_paintable().get_width(), 12)

    def test_scrolled_out_art_only_requests_after_entering_viewport(self):
        ui, sent = self.make_ui()
        scroll = Gtk.ScrolledWindow()
        area = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        spacer = Gtk.Box(height_request=1000)
        area.append(spacer)
        cover = ui.art_build(5, 40)
        area.append(cover)
        scroll.set_child(area)
        ui.win.set_child(scroll)
        ui.win.present()
        flush()
        state = {'Favorites': [{'ID': 's:one'}, {'ID': 's:two'}], 'Art': {'Epoch': 1}}
        ui.art_apply(state)
        self.assertEqual(sent, [])
        bar = scroll.get_vadjustment()
        self.assertGreater(bar.get_upper(), bar.get_page_size())
        bar.set_value(bar.get_upper() - bar.get_page_size())
        # Viewport transforms are applied during a frame, not synchronously
        # from GtkAdjustment::value-changed.
        loop = GLib.MainLoop()
        deadline = GLib.get_monotonic_time() + 1_000_000

        def wait_for_allocation():
            if ui._art_visible(cover) or GLib.get_monotonic_time() >= deadline:
                loop.quit()
                return False
            return True

        GLib.timeout_add(10, wait_for_allocation)
        loop.run()
        ui.art_apply(state)
        self.assertEqual(sent, [('art', {'ID': 's:two', 'Text': 'Favorites', 'Index': 1})])

    def test_follow_scrolls_long_lyrics_without_rebuilding_rows(self):
        ui, _ = self.make_ui()
        ui.stage_scroll = Gtk.ScrolledWindow()
        ui.stage_text = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        ui.stage_lines = [Gtk.Label(label=f'line {i}', height_request=30) for i in range(32)]
        for row in ui.stage_lines:
            ui.stage_text.append(row)
        ui.stage_scroll.set_child(ui.stage_text)
        ui.win.set_child(ui.stage_scroll)
        ui.win.present()
        flush()
        adj = ui.stage_scroll.get_vadjustment()
        self.assertEqual(adj.get_value(), 0)
        # Test centering without depending on Wayland animation frame delivery.
        settings = ui.stage_scroll.get_settings()
        previous = settings.get_property('gtk-enable-animations')
        self.addCleanup(settings.set_property, 'gtk-enable-animations', previous)
        settings.set_property('gtk-enable-animations', False)
        ui.art_follow_lyric(20)
        loop = GLib.MainLoop()
        deadline = GLib.get_monotonic_time() + 2_000_000

        def wait_for_scroll():
            if adj.get_value() > 500 or GLib.get_monotonic_time() >= deadline:
                loop.quit()
                return False
            return True

        GLib.timeout_add(16, wait_for_scroll)
        loop.run()
        self.assertGreater(adj.get_value(), 500)
        self.assertIs(ui.stage_lines[20].get_parent(), ui.stage_text)

    def test_lyric_offset_updates_cues_without_rewriting_labels(self):
        ui, sent = self.make_ui()
        ui.state = {'Position': 0.75, 'Lyrics': [{'time': 1, 'text': 'one'},
                   {'time': 2, 'text': ''}, {'time': 3, 'text': 'two', 'translation': '二'}],
                   'Art': {'Epoch': 1, 'LyricToken': 'current'}}
        ui.art_apply(ui.state)
        self.assertEqual(ui.art_lyric_lines(), [('one',), ('two', '二')])
        self.assertEqual(ui.art_lyric_current(), -1)
        ui._art_offset(1)
        self.assertEqual(sent, [('lyric-offset', {'ID': 'current', 'Index': 1})])
        ui.state['Art']['LyricOffset'] = .5
        self.assertEqual(ui.art_lyric_current(), 0)
        ui.state['Position'] = 1.75
        self.assertEqual(ui.art_lyric_current(), -1)


if __name__ == '__main__':
    unittest.main()
