# Stop if this powershell script is not run as admin
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    [System.Windows.Forms.Messagebox]::Show("Not running as administrator!");
}

# Download and install chocolatey (Windows Package Manager)

Push-Location ~/Downloads

$url = "https://chocolatey.org/install.ps1 "
$output = ".\install-chocolatey.ps1"
Invoke-WebRequest -Uri $url -OutFile $output

nvoke-Expression ".\install-chocolatey.ps1"

# Install git (Gitlab Runner prerequisity)
Invoke-Expression "choco install git -y"

# Install Gitlab Runner
Invoke-Expression "choco install gitlab-runner -y"

Pop-Location

# Install Gitlab Runner Service

Push-Location "~\Documents"

## Read prepared token
$tokenPath = ".\gitlab-registration-token.txt"
$token = Get-Content $tokenPath -Raw 

Pop-Location

Push-Location "C:\gitlab-runner"

# Register runner
Invoke-Expression "cmd /c gitlab-runner.exe register --non-interactive --url https://gitlab.com/ --registration-token $token --executor shell --description Win10-Server-Hetzner --tag-list windows10"

# Install and run the service
Invoke-Expression "cmd /c gitlab-runner.exe install"
Invoke-Expression "cmd /c gitlab-runner.exe start"

Pop-Location
