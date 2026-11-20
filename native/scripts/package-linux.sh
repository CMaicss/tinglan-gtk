#!/usr/bin/env bash
set -euo pipefail

# Package the same tested Linux binary in all three formats.
if [[ $# != 3 || ! $2 =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf 'Usage: %s <linux-amd64-binary> <version> <output-directory>\n' "$0" >&2
    exit 2
fi
binary=$(realpath "$1")
version=$2
mkdir -p "$3"
out=$(realpath "$3")
repo=$(cd -- "$(dirname -- "$0")/../.." && pwd)
: "${APPIMAGETOOL:?set APPIMAGETOOL to the verified x86_64 AppImage tool}"
: "${APPIMAGE_RUNTIME:?set APPIMAGE_RUNTIME to the verified type-2 runtime}"
for cmd in dpkg-deb rpmbuild rpm2cpio cpio desktop-file-validate readelf file; do command -v "$cmd" >/dev/null; done
LC_ALL=C readelf -h "$binary" | grep -q 'Advanced Micro Devices X86-64'
stage=$(mktemp -d "${TMPDIR:-/tmp}/tinglan-package.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
app="$stage/AppDir"
icon=org.tinglan.unofficial.gtk
install -Dm755 "$binary" "$app/usr/bin/tinglan"
install -Dm644 "$repo/build/icon.png" "$app/usr/share/icons/hicolor/512x512/apps/$icon.png"
install -Dm644 "$repo/LICENSE" "$app/usr/share/doc/tinglan/LICENSE"
install -Dm644 "$repo/NOTICE" "$app/usr/share/doc/tinglan/NOTICE"
install -Dm644 "$repo/build/icon.LICENSE.txt" "$app/usr/share/doc/tinglan/icon.LICENSE.txt"
mkdir -p "$app/usr/share/applications"
cat > "$app/usr/share/applications/$icon.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=听澜
Name[en]=Tinglan
Comment=Unofficial desktop music player
Exec=tinglan
Icon=$icon
Terminal=false
Categories=AudioVideo;Audio;Player;
EOF
desktop-file-validate "$app/usr/share/applications/$icon.desktop"

cp -a "$app" "$stage/deb"
mkdir "$stage/deb/DEBIAN"
cat > "$stage/deb/DEBIAN/control" <<EOF
Package: tinglan
Version: $version-1
Architecture: amd64
Maintainer: Tinglan GTK contributors
Section: sound
Priority: optional
Depends: libc6, libasound2t64 | libasound2, python3, python3-gi, gir1.2-gtk-4.0, libadwaita-1-0 (>= 1.6), gir1.2-adw-1
Description: Unofficial GTK desktop music player
 Linux player using independently installed music-source plugins.
EOF
deb="tinglan_${version}-1_amd64.deb"
dpkg-deb --root-owner-group --build "$stage/deb" "$stage/$deb"

mkdir -p "$stage/rpm"
cat > "$stage/tinglan.spec" <<EOF
Name: tinglan
Version: $version
Release: 1
Summary: Unofficial GTK desktop music player
License: AGPL-3.0-only
BuildArch: x86_64
Requires: python3, python3-gobject, gtk4, libadwaita >= 1.6, alsa-lib
%global debug_package %{nil}
%global __os_install_post %{nil}
%description
Linux player using independently installed music-source plugins.
%install
mkdir -p "%{buildroot}"
cp -a "$app/usr" "%{buildroot}/"
%files
/usr/bin/tinglan
/usr/share/applications/$icon.desktop
/usr/share/icons/hicolor/512x512/apps/$icon.png
/usr/share/doc/tinglan/
EOF
rpmbuild -bb --define "_topdir $stage/rpm" "$stage/tinglan.spec"
rpm="tinglan-${version}-1.x86_64.rpm"
cp "$stage/rpm/RPMS/x86_64/$rpm" "$stage/$rpm"

ln -s "usr/share/applications/$icon.desktop" "$app/$icon.desktop"
ln -s "usr/share/icons/hicolor/512x512/apps/$icon.png" "$app/$icon.png"
cat > "$app/AppRun" <<'EOF'
#!/bin/sh
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$root/usr/bin/tinglan" "$@"
EOF
chmod 755 "$app/AppRun"
image="tinglan-${version}-x86_64.AppImage"
ARCH=x86_64 APPIMAGE_EXTRACT_AND_RUN=1 "$APPIMAGETOOL" --runtime-file "$APPIMAGE_RUNTIME" --mksquashfs-opt=-processors --mksquashfs-opt=2 "$app" "$stage/$image"

# Verify payload and desktop entry in all three real archives before publishing files.
mkdir "$stage/check-deb" "$stage/check-rpm" "$stage/check-image"
dpkg-deb -x "$stage/$deb" "$stage/check-deb"
(cd "$stage/check-rpm" && rpm2cpio "$stage/$rpm" | cpio -id --quiet)
(cd "$stage/check-image" && "$stage/$image" --appimage-extract >/dev/null)
for root in "$stage/check-deb" "$stage/check-rpm" "$stage/check-image/squashfs-root"; do
    cmp "$binary" "$root/usr/bin/tinglan"
    test -x "$root/usr/bin/tinglan"
    desktop-file-validate "$root/usr/share/applications/$icon.desktop"
    cmp "$repo/build/icon.png" "$root/usr/share/icons/hicolor/512x512/apps/$icon.png"
done
(cd "$stage" && sha256sum "$deb" "$rpm" "$image" > SHA256SUMS)
for file in "$deb" "$rpm" "$image" SHA256SUMS; do mv "$stage/$file" "$out/$file"; done
(cd "$out" && sha256sum -c SHA256SUMS)
