## Verifying Release Signatures

If a verification step fails, please contact https://support.sia.tech/ for
additional information or to find a support contact before using the binary 

1. First you need to download and import the correct `gpg` key. This key will not be changed without advanced notice.
  - `wget -c https://gitlab.com/NebulousLabs/Sia/doc/developer-pubkeys/sia-signing-key.asc`
  - `gpg --import sia-signing-key.asc`

2. Download the `SHA256SUMS` file for the release.
	- `wget -c http://gitlab.com/NebulousLabs/Sia/doc/Sia-1.x.x-SHA256SUMS.txt.asc`

3. Verify the signature.
   - `gpg --verify Sia-1.x.x-SHA256SUMS.txt.asc`
   
   **If the output of that command fails STOP AND DO NOT USE THAT BINARY.**

4. Hash your downloaded binary.
	- `shasum256 ./siad` or similar, providing the path to the binary as the first argument.
	 
   **If the output of that command is not found in `Sia-1.x.x-SHA256SUMS.txt.asc` STOP AND DO NOT USE THAT BINARY.**
