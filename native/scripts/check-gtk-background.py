#!/usr/bin/env python3
"""Check hidden GTK playback in a Linux container without a real sound card."""

import argparse
import os
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=lambda path: Path(path).resolve(strict=True))
    args = parser.parse_args()

    with tempfile.TemporaryDirectory(prefix='tinglan-gtk-background-') as tmp:
        fixture = Path(tmp)
        alsa = fixture / 'alsa.conf'
        alsa.write_text('pcm.!default { type null }\n')
        for name, frequency, duration in [('tone.mp3', 440, 1), ('tone.flac', 550, 4)]:
            subprocess.run([
                'ffmpeg', '-nostdin', '-loglevel', 'error', '-f', 'lavfi', '-i',
                f'sine=frequency={frequency}:duration={duration}', str(fixture / name),
            ], check=True, timeout=10)
        env = dict(os.environ, ALSA_CONFIG_PATH=str(alsa), GDK_BACKEND='x11',
                   GTK_A11Y='none', GSK_RENDERER='cairo', XDG_CONFIG_HOME=tmp)
        result = subprocess.run([
            'xvfb-run', '-a', str(args.binary), '--demo', '2', '--fixture-dir',
            tmp, '--check-background',
        ], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=20)
        print(result.stdout, end='')
        if result.returncode or 'BACKGROUND_CHECK_OK: GTK hidden window advanced to track 2' not in result.stdout.splitlines():
            raise SystemExit('GTK hidden playback check failed')


if __name__ == '__main__':
    main()
