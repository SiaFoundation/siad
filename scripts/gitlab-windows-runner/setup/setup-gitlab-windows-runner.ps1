# Stop if this powershell script is not run as admin
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    [System.Windows.Forms.Messagebox]::Show("Not running as administrator!");
}

# Download and install chocolatey (Windows Package Manager)

Push-Location ~/Downloads

$url = "https://chocolatey.org/install.ps1 "
$output = ".\install-chocolatey.ps1"
Invoke-WebRequest -Uri $url -OutFile $output

Invoke-Expression ".\install-chocolatey.ps1"

# Install git (Gitlab Runner prerequisity)
choco install git -y

# Install golang
choco install golang -y

# Install make
choco install make -y

# Install Gitlab Runner
choco install gitlab-runner -y

# Install Dokany (FUSE for Windows)
choco install dokany -y

Pop-Location

# Install Gitlab Runner Service

Push-Location "~\Documents"

## Read prepared variables
$tokenPath = ".\gitlab-registration-token.txt"
$token = Get-Content $tokenPath -Raw

Pop-Location

Push-Location "C:\gitlab-runner"

# Register runner
# Use pre-clone-script to fix gitlab cache permissions 
# (Permission denied issues on cloning git repo)
gitlab-runner.exe register --non-interactive --url https://gitlab.com/ --registration-token $token --executor shell --pre-clone-script "remove-item .\builds\*\*\*\*\.cache -Force -Recurse -ErrorAction SilentlyContinue" --description Win10-Server-Hetzner --tag-list nebulous-windows

# Install and run the service

gitlab-runner.exe install
gitlab-runner.exe start

Pop-Location
