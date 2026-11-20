#!/usr/bin/env python3
"""Exercise the experimental GTK executable on an isolated Xvfb display."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


def until(check, label, timeout=5):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = check()
        if value is not None:
            return value
        time.sleep(0.05)
    raise RuntimeError(f'timed out: {label}')


def xdo(*args):
    return subprocess.check_output(['xdotool', *args], text=True, timeout=3).strip()


def exercise(binary):
    with tempfile.TemporaryDirectory(prefix='tinglan-gtk-check-') as tmp:
        profile = Path(tmp) / 'profile'
        profile.mkdir()
        (profile / 'queue.json').write_text(json.dumps({
            'tracks': [{'id': 'seed-a', 'title': 'Seed A'}, {'id': 'seed-b', 'title': 'Seed B'}],
            'index': 0,
        }))
        saved = profile / 'queue.local.json'
        expected = ['Seed A', 'Smoke MP3', 'Seed B']

        def queue_titles():
            if not saved.exists():
                return None
            doc = json.loads(saved.read_text())
            return [item['title'] for item in doc['items']], doc['index']

        def run(query, index, check_queue=False):
            env = dict(os.environ, GDK_BACKEND='x11', GTK_A11Y='none', GSK_RENDERER='cairo', XDG_CONFIG_HOME=tmp)
            with subprocess.Popen([binary, '--demo', '2', '--profile', str(profile)], env=env) as app:
                try:
                    def find():
                        if app.poll() is not None:
                            raise RuntimeError(f'GTK exited early ({app.returncode})')
                        try:
                            matches = xdo('search', '--onlyvisible', '--pid', str(app.pid), '--name', '听澜')
                        except subprocess.CalledProcessError:
                            return None
                        return matches.splitlines()[0] if matches else None
                    window = until(find, 'GTK window')
                    # No window manager in Xvfb. Coordinates are window-relative,
                    # at the fixed initial size of this experimental layout.
                    xdo('windowfocus', window, 'mousemove', '--window', window, '85', '81', 'click', '1', 'mousemove', '--window', window, '638', '130', 'click', '1')
                    xdo('type', '--clearmodifiers', query)
                    xdo('key', 'Return')
                    def activate_and_check():
                        xdo('mousemove', '--window', window, '362', '180', 'click', '1')
                        got = queue_titles()
                        return got if got == (expected, index) else None
                    until(activate_and_check, f'{query}: queue selection {index}')
                    if not check_queue:
                        return

                    def click(x, y, target=window):
                        xdo('mousemove', '--window', target, str(x), str(y), 'click', '1')

                    def expect(titles, selection):
                        until(lambda: True if queue_titles() == (titles, selection) else None,
                              f'queue is {titles} at {selection}')

                    click(1100, 713)  # Open queue panel.
                    time.sleep(.25)
                    click(900, 137)  # Select Seed A.
                    expect(expected, 0)
                    click(1115, 137)  # Remove the current track.
                    remaining = expected[1:]
                    expect(remaining, 0)

                    def dialog_window():
                        visible = xdo('search', '--onlyvisible', '--pid', str(app.pid)).splitlines()
                        return next((candidate for candidate in visible if candidate != window), None)

                    click(1079, 77)
                    dialog = until(dialog_window, 'clear confirmation')
                    click(80, 102, dialog)
                    until(lambda: True if dialog_window() is None else None, 'cancel closes confirmation')
                    expect(remaining, 0)
                    click(1079, 77)
                    dialog = until(dialog_window, 'clear confirmation reopened')
                    click(230, 102, dialog)
                    expect([], 0)

                    # The accepted compact layout fits 400 px. Hidden pages
                    # must not impose their larger minimum on the search page.
                    xdo('windowsize', window, '400', '680')
                    def compact_geometry():
                        geometry = xdo('getwindowgeometry', '--shell', window)
                        return True if {'WIDTH=400', 'HEIGHT=680'} <= set(geometry.splitlines()) else None
                    until(compact_geometry, 'compact 400 × 680 GTK layout')
                finally:
                    if app.poll() is None:
                        app.terminate()
                        try:
                            app.wait(timeout=3)
                        except subprocess.TimeoutExpired:
                            app.kill()
        run('MP3', 1)
        # Reopen the same test profile and select a pre-existing result. Do not
        # infer persistence just from the first process writing the file.
        expected[:] = ['Seed A', 'Smoke MP3', 'Smoke FLAC', 'Seed B']
        run('FLAC', 2, check_queue=True)
        print('PASS: GTK search/restart preserves queue order; select, remove, cancel and confirm clear')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=lambda path: str(Path(path).resolve(strict=True)))
    parser.add_argument('--_isolated', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args._isolated:
        exercise(args.binary)
        return 0
    return subprocess.call(['xvfb-run', '-a', '-s', '-screen 0 1280x900x24', sys.executable, __file__, args.binary, '--_isolated'])


if __name__ == '__main__':
    raise SystemExit(main())
