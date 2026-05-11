# build.ps1 — cross-compile myself-ai for all target platforms (Windows)
$ErrorActionPreference = "Stop"

$Binary   = "myself-ai"
$DistDir  = "dist"
$Pkg      = "github.com/1344011985/MyselfAI/internal/command"

$Commit = (git rev-parse --short HEAD 2>$null) -replace "`n",""
if (-not $Commit) { $Commit = "unknown" }
$Date = (Get-Date -Format "yyyy-MM-ddTHH:mm:ssZ")
$LdFlags = "-X ${Pkg}.GitCommit=${Commit} -X ${Pkg}.BuildDate=${Date}"

$Platforms = @(
    @{ OS = "linux";   Arch = "amd64" },
    @{ OS = "linux";   Arch = "arm64" },
    @{ OS = "windows"; Arch = "amd64" },
    @{ OS = "darwin";  Arch = "amd64" },
    @{ OS = "darwin";  Arch = "arm64" }
)

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

foreach ($p in $Platforms) {
    $os   = $p.OS
    $arch = $p.Arch
    $output = "$DistDir\${Binary}-${os}-${arch}"

    if ($os -eq "windows") {
        $output = "$output.exe"
    }

    Write-Host "Building $output ..."
    $env:GOOS   = $os
    $env:GOARCH = $arch
    & go build -ldflags $LdFlags -o $output ./cmd/bot/
    if ($LASTEXITCODE -ne 0) { throw "Build failed for ${os}/${arch}" }

    # Package
    if ($os -eq "windows") {
        Compress-Archive -Path $output -DestinationPath "$DistDir\${Binary}-${os}-${arch}.zip" -Force
    } else {
        # tar is available on Windows 10+ and in Git Bash
        $tarName = "${Binary}-${os}-${arch}"
        tar -czf "$DistDir\${tarName}.tar.gz" -C $DistDir $tarName
    }
}

Write-Host "Done. Artifacts in $DistDir/"
