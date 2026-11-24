import unittest
from types import SimpleNamespace

from live_collections import collections_mixin


class Sink:
    def __init__(self):
        self.state: dict = {'Results': [{'ID': 'provider:1'}, {'ID': 'provider:2'}]}
        self.commands = []
        self.stopped = False
        self.view = 'playlists'

    def send(self, op, **data):
        self.commands.append((op, data))


class TestLiveCollectionsContract(unittest.TestCase):
    def setUp(self):
        class Frontend(collections_mixin(SimpleNamespace()), Sink):
            pass
        self.ui = Frontend()

    def test_row_action_uses_captured_identity_not_a_reordered_row(self):
        self.ui.state['Results'].reverse()
        self.ui.collection_track_action('favorite', 'Results', 0, 'provider:1')
        self.assertEqual(self.ui.commands, [])

    def test_playlist_picker_dispatches_id_and_row_not_title(self):
        self.ui.collection_track_action('add', 'Results', 1, 'provider:2', 'local-id')
        self.assertEqual(self.ui.commands, [('collection-add-Results',
            dict(Index=1, ID='provider:2', Text='local-id'))])

    def test_open_playlist_uses_captured_card_id(self):
        self.ui.state.update(Collections={}, Playlists=[{'ID': 'original'}])
        self.ui.open_playlist(0, 'name', 'local')
        self.assertEqual(self.ui.commands, [('collection-open', dict(ID='original'))])


class TestLiveCollectionsGTK(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        try:
            import app as preview
        except (ImportError, ValueError) as err:
            raise unittest.SkipTest(f'GTK bindings unavailable: {err}') from err
        if not preview.Gtk.init_check() or not hasattr(preview.Adw, 'AlertDialog'):
            raise unittest.SkipTest('GTK display and libadwaita AlertDialog required')
        cls.preview = preview

    def test_track_labels_align_with_larger_cover_and_preserve_row_actions(self):
        d = self.preview

        class Base(d.Preview):
            live = True
            def __init__(self):
                self.state = {'Results': [{'ID':'provider:song', 'Title':'Song', 'Artist':'Artist', 'Album':'Album'}],
                              'Collections': {'CanEdit': True}}
                self.stopped = False
                self.view = 'search'
                self.search_results = d.box(True)
                self.track_rows = []
                self.commands = []

            def send(self, op, **data):
                self.commands.append((op, data))

            def play_from_list(self, n, append=False):
                self.commands.append(('row-play', (n, append)))

        class Frontend(collections_mixin(d), Base):
            pass

        ui = Frontend()
        original = d.TRACKS
        try:
            d.TRACKS = [('Song', 'Artist', 'Album', '1:00')]
            listing = ui.add_tracks(ui.search_results, [0], favorite=False, add_to_queue=True)
        finally:
            d.TRACKS = original
        row = listing.get_first_child()
        names = row.get_child().get_first_child().get_next_sibling()
        self.assertIs(names.get_next_sibling(), row.collection_star[1])
        title = names.get_first_child()
        self.assertIsInstance(title, d.Gtk.Label)
        self.assertEqual(title.get_text(), 'Song')
        self.assertFalse(title.get_selectable())
        self.assertEqual(ui.commands, [])
        artist = names.get_last_child()
        self.assertIsInstance(artist, d.Gtk.Label)
        self.assertEqual(artist.get_text(), 'Artist')
        self.assertFalse(artist.get_selectable())

        window = d.Gtk.Window(default_width=420, child=ui.search_results)
        self.addCleanup(window.destroy)
        window.present()
        loop = d.GLib.MainLoop()
        d.GLib.timeout_add(100, lambda: (loop.quit(), d.GLib.SOURCE_REMOVE)[1])
        loop.run()
        line = row.get_child()
        cover = line.get_first_child()
        self.assertEqual((cover.get_width(), cover.get_height()), (48, 48))
        positions = []
        for widget in (cover, title, artist):
            ok, bounds = widget.compute_bounds(line)
            self.assertTrue(ok)
            positions.append(bounds.get_x())
        self.assertEqual(positions[1], positions[2])
        self.assertEqual(positions[1] - positions[0] - cover.get_width(), 14)

        listing.emit('row-activated', row)
        menu = line.get_last_child().get_popover().get_child()
        self.assertEqual(menu.get_first_child().get_label(), '专辑 · Album')
        artist_action = menu.get_first_child().get_next_sibling()
        self.assertEqual(artist_action.get_label(), '艺人 · Artist')
        artist_action.emit('clicked')
        row.collection_star[1].emit('clicked')
        self.assertEqual(ui.commands, [
            ('row-play', (0, True)),
            ('collection-artist-Results', dict(ID='provider:song', Index=0, Text='Artist')),
            ('collection-favorite-Results', dict(ID='provider:song', Index=0, Text='')),
        ])


if __name__ == '__main__':
    unittest.main()
