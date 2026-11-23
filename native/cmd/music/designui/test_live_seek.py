import io
import json
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import app as d
from live import Live


class LiveSeekTest(unittest.TestCase):
    def test_drag_previews_until_release_and_drops_stale_target(self):
        d.Adw.init()
        application = d.Adw.Application(application_id='org.tinglan.TestSeek', flags=d.Gio.ApplicationFlags.NON_UNIQUE)
        application.register(None)
        backend = SimpleNamespace(stdin=io.StringIO())
        args = SimpleNamespace(backend=Path('/unused'), width=1140, height=760,
                               page='favorites', queue=False, smoke=False, light=False, dark=False)
        with patch('live.subprocess.Popen', return_value=backend), patch('live.threading.Thread'):
            ui = Live(application, args, d.Preview)
        try:
            ui.state = {'Current': 0, 'Queue': [{'ID': 'fixture:one'}]}
            ui.seek.allocate(400, 40, -1, None)
            ui.sync_seek(120, 10)
            backend.stdin.seek(0)
            backend.stdin.truncate()
            drag = ui.seek_drag
            drag.emit('drag-begin', 40., 20.)
            drag.emit('drag-update', 120., 0.)
            drag.emit('drag-update', 180., 0.)
            preview = ui.seek.get_value()
            self.assertGreater(preview, 50)
            ui.sync_seek(120, 12)
            self.assertEqual(ui.seek.get_value(), preview)
            self.assertEqual(backend.stdin.getvalue(), '')
            drag.emit('drag-end', 180., 0.)
            self.assertEqual(json.loads(backend.stdin.getvalue()), {'Op': 'seek', 'Value': preview})

            backend.stdin.seek(0)
            backend.stdin.truncate()
            drag.emit('drag-begin', 20., 20.)
            ui.state = {'Current': 0, 'Queue': [{'ID': 'fixture:two'}]}
            ui.sync_seek(80, 3)
            drag.emit('drag-end', 100., 0.)
            self.assertEqual(backend.stdin.getvalue(), '')
            self.assertEqual(ui.seek.get_value(), 3)
        finally:
            ui.quit_window()
