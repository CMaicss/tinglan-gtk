import base64
import unittest
from types import SimpleNamespace

from live_art import art_mixin

import gi
gi.require_version('Gtk', '4.0')
gi.require_version('Adw', '1')
from gi.repository import Gtk, Gdk, GLib


def png_fixture():
    pixel = GLib.Bytes.new(bytes((255, 0, 0, 255)))
    texture = Gdk.MemoryTexture.new(1, 1, Gdk.MemoryFormat.R8G8B8A8, pixel, 4)
    return base64.b64encode(texture.save_to_png_bytes().get_data()).decode('ascii')


def flush():
    context = GLib.MainContext.default()
    while context.pending():
        context.iteration(False)


class DesignArtDeliveryTest(unittest.TestCase):
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
        ui.art_follow_lyric(20)
        loop = GLib.MainLoop()
        GLib.timeout_add(450, lambda: (loop.quit(), False)[1])
        loop.run()
        self.assertGreater(adj.get_value(), 500)
        self.assertIs(ui.stage_lines[20].get_parent(), ui.stage_text)

    def test_lyric_offset_updates_cues_without_rewriting_labels(self):
        ui, sent = self.make_ui()
        ui.state = {'Position': 0.75, 'Lyrics': [{'time': 1, 'text': 'one'},
                   {'time': 2, 'text': ''}, {'time': 3, 'text': 'two', 'translation': '二'}],
                   'Art': {'Epoch': 1, 'LyricToken': 'current'}}
        ui.art_apply(ui.state)
        self.assertEqual(ui.art_lyric_lines(), ['one', 'two\n二'])
        self.assertEqual(ui.art_lyric_current(), -1)
        ui._art_offset(1)
        self.assertEqual(sent, [('lyric-offset', {'ID': 'current', 'Index': 1})])
        ui.state['Art']['LyricOffset'] = .5
        self.assertEqual(ui.art_lyric_current(), 0)
        ui.state['Position'] = 1.75
        self.assertEqual(ui.art_lyric_current(), -1)


if __name__ == '__main__':
    unittest.main()
