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

    def test_title_and_artist_buttons_dispatch_distinct_actions(self):
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
        names.get_first_child().emit('clicked')
        names.get_last_child().emit('clicked')
        row.collection_star[1].emit('clicked')
        self.assertEqual(ui.commands, [
            ('collection-title-Results', dict(ID='provider:song', Index=0, Text='')),
            ('collection-artist-Results', dict(ID='provider:song', Index=0, Text='Artist')),
            ('collection-favorite-Results', dict(ID='provider:song', Index=0, Text='')),
        ])


if __name__ == '__main__':
    unittest.main()
