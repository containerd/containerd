Write-Output '::group::Update chocolaty'
choco upgrade -y chocolatey
Write-Output '::endgroup::'

if ( $LASTEXITCODE ) {
    Write-Output '::error::Could not update chocolatey.'
    exit $LASTEXITCODE
}
