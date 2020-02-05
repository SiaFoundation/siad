#!/usr/bin/env bash
set -e

version="$1"

# Directory where the binaries produces by build-release.sh are stored.
binDir="$2"

# Directory of the Sia-UI Repo.
uiDir="$3"
if [ -z "$version" ] || [ -z "$binDir" ] || [ -z "$uiDir" ]; then
  echo "Usage: $0 VERSION BIN_DIRECTORY UI_DIRECTORY"
  exit 1
fi

echo Version: "${version}"
echo Binaries Directory: "${binDir}"
echo UI Directory: "${uiDir}"
echo ""
echo "Is this okay? (sleeping..)"
sleep 3

# Get the absolute paths to avoid funny business with relative paths.
uiDir=$(realpath "${uiDir}")
binDir=$(realpath "${binDir}")

# Remove previously built UI binaries.
rm -r "${uiDir}"/release/

cd "${uiDir}"

# Copy over all the siac/siad binaries.
mkdir -p bin/{linux,mac,win}
cp "${binDir}"/Sia-"${version}"-darwin-amd64/siac bin/mac/
cp "${binDir}"/Sia-"${version}"-darwin-amd64/siad bin/mac/

cp "${binDir}"/Sia-"${version}"-linux-amd64/siac bin/linux/
cp "${binDir}"/Sia-"${version}"-linux-amd64/siad bin/linux/

cp "${binDir}"/Sia-"${version}"-windows-amd64/siac.exe bin/win/
cp "${binDir}"/Sia-"${version}"-windows-amd64/siad.exe bin/win/

# Build yarn deps.
yarn

# Build each of the UI binaries.
yarn package-linux
yarn package-win
yarn package

# Copy the UI binaries into the binDir. Also change the name at the same time.
# The UI builder doesn't handle 4 digit versions correctly and if the UI hasn't
# been updated the version might be stale.
for ext in AppImage dmg exe; do 
  mv "${uiDir}"/release/*."${ext}" "${binDir}"/Sia-UI-"${version}"."${ext}"
	(
		cd "${binDir}"
		sha256sum Sia-UI-"${version}"."${ext}" >> Sia-"${version}"-SHA256SUMS.txt
	)
done
