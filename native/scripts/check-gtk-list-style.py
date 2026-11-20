#!/usr/bin/env python3
"""Check actual GTK list pixels on an isolated Xvfb display (Python, ffmpeg, xdotool)."""
import os
import subprocess as sp
import sys
import tempfile
import time
from pathlib import Path


def run(*args):
    return sp.check_output(args, timeout=8)


def frame(binary, appearance):
    with tempfile.TemporaryDirectory(prefix='tinglan-style-') as profile:
        env = dict(os.environ, GDK_BACKEND='x11', GTK_A11Y='none', GSK_RENDERER='cairo',
                   GTK_THEME='Adwaita:' + appearance, XDG_CONFIG_HOME=profile)
        app = sp.Popen([binary, '--demo', '10'], env=env)
        try:
            window = run('xdotool', 'search', '--sync', '--onlyvisible', '--pid', str(app.pid), '--name', '听澜').decode().splitlines()[0]
            run('xdotool', 'mousemove', '--window', window, '170', '350')
            time.sleep(.45)
            pixels = run('ffmpeg', '-loglevel', 'error', '-f', 'x11grab', '-draw_mouse', '0', '-video_size', '1140x760',
                         '-i', os.environ['DISPLAY'], '-frames:v', '1', '-pix_fmt', 'rgb24', '-f', 'rawvideo', '-')
            def pixel(x, y):
                i = 3 * (1140 * y + x)
                return tuple(pixels[i:i+3])
            page, row = pixel(960, 151), pixel(960, 211)
            print(f'{appearance}: page={page}, list={row}', flush=True)
            assert max(abs(a - b) for a, b in zip(page, row)) <= 2, 'list should inherit the page surface'
        finally:
            app.terminate()
            app.wait(timeout=5)


if __name__ == '__main__':
    if len(sys.argv) == 2:
        sys.exit(sp.call(['xvfb-run', '-a', '-s', '-screen 0 1280x900x24', sys.executable, __file__,
                          str(Path(sys.argv[1]).resolve(strict=True)), '--isolated']))
    for variant in ('light', 'dark'):
        frame(sys.argv[1], variant)
