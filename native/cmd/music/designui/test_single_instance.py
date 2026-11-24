"""Run with dbus-run-session -- /usr/bin/python3 test_single_instance.py /path/to/tinglan.

Uses a private Broadway display and an empty profile with the real Go backend.
"""
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
from time import monotonic, sleep


def worker(backend, events, *options):
    from app import main
    import live
    from gi.repository import GLib, GLibUnix

    create = live.Live

    def observe(*args):
        ui = create(*args)

        def record(event):
            with events.open('a') as log:
                print(event, ui.backend.pid, file=log)

        def ready():
            if not ui.state.get('Settings'):
                return GLib.SOURCE_CONTINUE
            record('ready')
            return GLib.SOURCE_REMOVE

        def hide():
            ui.win.close()
            return GLib.SOURCE_CONTINUE

        record('started')
        ui.win.connect('map', lambda *_: record('mapped'))
        ui.win.connect('unmap', lambda *_: record('unmapped'))
        GLibUnix.signal_add(GLib.PRIORITY_DEFAULT, signal.SIGUSR1, hide)
        GLib.timeout_add(30, ready)
        return ui

    live.Live = observe
    sys.argv = [__file__, '--backend', backend, *options]
    return main()


def wait_for(check, description):
    deadline = monotonic() + 8
    while monotonic() < deadline:
        if result := check():
            return result
        sleep(.03)
    raise AssertionError(description)


def stop(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


def check_single_instance(backend):
    with tempfile.TemporaryDirectory(prefix='tl-') as directory:
        root = Path(directory)
        env = dict(os.environ, GDK_BACKEND='broadway', BROADWAY_DISPLAY=':0', GTK_A11Y='none',
                   XDG_RUNTIME_DIR=directory, XDG_CONFIG_HOME=str(root / 'config'),
                   XDG_CACHE_HOME=str(root / 'cache'), XDG_DATA_HOME=str(root / 'data'))
        env.pop('VIVA_HEADLESS', None)
        display = subprocess.Popen(['gtk4-broadwayd', '-a', '127.0.0.1', '-p', '0', ':0'], env=env)
        cmd = [sys.executable, __file__, '--worker', str(Path(backend).resolve())]
        events = root / 'primary'
        try:
            wait_for((root / 'broadway1.socket').exists, 'private display failed to start')
            primary = subprocess.Popen([*cmd, str(events)], env=env)
            try:
                def read_events():
                    return events.read_text().splitlines() if events.exists() else []

                wait_for(lambda: len(read_events()) >= 3, 'primary window/backend did not start')
                started, mapped, ready = read_events()
                pid = ready.split()[1]
                assert [started, mapped, ready] == [f'started {pid}', f'mapped {pid}', f'ready {pid}']
                primary.send_signal(signal.SIGUSR1)
                wait_for(lambda: f'unmapped {pid}' in read_events(), 'close-to-tray did not hide the window')
                for name in ('while-hidden', 'while-visible'):
                    secondary_events = root / name
                    secondary = subprocess.Popen([*cmd, str(secondary_events)], env=env)
                    try:
                        assert secondary.wait(timeout=5) == 0
                        assert not secondary_events.exists(), 'duplicate launch created a window/backend'
                    except subprocess.TimeoutExpired as error:
                        raise AssertionError('duplicate launch did not exit after activation') from error
                    finally:
                        stop(secondary)
                smoke_events = root / 'smoke'
                smoke = subprocess.run([*cmd, str(smoke_events), '--smoke'], env=env,
                                       capture_output=True, text=True, timeout=5)
                assert smoke.returncode != 0, 'busy --smoke falsely reported success'
                assert '请先退出' in smoke.stderr, smoke.stderr
                assert not smoke_events.exists(), 'busy --smoke started another backend'
                assert read_events() == [started, mapped, ready, f'unmapped {pid}', mapped], read_events()
                os.kill(int(pid), 0)
                print('PASS: hidden window remapped, visible window reused, same backend PID')
            finally:
                stop(primary)
        finally:
            stop(display)


if __name__ == '__main__':
    if sys.argv[1] == '--worker':
        raise SystemExit(worker(sys.argv[2], Path(sys.argv[3]), *sys.argv[4:]))
    check_single_instance(sys.argv[1])
