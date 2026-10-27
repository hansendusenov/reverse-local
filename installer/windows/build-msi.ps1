#Requires -Version 5.1
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
$RepoRoot = (Resolve-Path (Join-Path $ScriptDir "..\..")).Path
$DistDir = Join-Path $RepoRoot "dist"
New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

$ExePath = Join-Path $RepoRoot "reverse-local.exe"
$IcoPath = Join-Path $RepoRoot "internal\assets\app.ico"
if (-not (Test-Path $ExePath)) {
    throw "reverse-local.exe not found at $ExePath."
}
if (-not (Test-Path $IcoPath)) {
    throw "app.ico not found at $IcoPath. Run 'go run cmd/genassets/main.go' first."
}

$MsiPath = Join-Path $DistDir "ReverseLocal-1.2.0-x64.msi"
if (Test-Path $MsiPath) {
    Remove-Item -Force $MsiPath
}

# 1. Ensure a local Authenticode Code Signing Certificate exists and sign the target binary/MSI
function Ensure-AuthenticodeSignature {
    param([string]$TargetFile)
    try {
        $certSubject = "CN=Hansen Dusenov (Reverse.Local), O=ReverseLocal Development"
        $cert = Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert | Where-Object { $_.Subject -eq $certSubject } | Select-Object -First 1
        if (-not $cert) {
            $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject $certSubject -KeyAlgorithm RSA -KeyLength 2048 -HashAlgorithm SHA256 -CertStoreLocation "Cert:\CurrentUser\My" -NotAfter (Get-Date).AddYears(5)
        }
        $store = New-Object System.Security.Cryptography.X509Certificates.X509Store("TrustedPublisher", "CurrentUser")
        $store.Open("ReadWrite")
        if (-not ($store.Certificates | Where-Object { $_.Thumbprint -eq $cert.Thumbprint })) {
            $store.Add($cert)
        }
        $store.Close()
        Set-AuthenticodeSignature -FilePath $TargetFile -Certificate $cert -HashAlgorithm SHA256 | Out-Null
    } catch {
        Write-Host "[WARN] Optional Authenticode signing skipped: $($_.Exception.Message)" -ForegroundColor Yellow
    }
}

Write-Host "[1/4] Signing reverse-local.exe with Authenticode certificate..." -ForegroundColor Cyan
Ensure-AuthenticodeSignature -TargetFile $ExePath

Write-Host "[2/4] Compressing reverse-local.exe & app.ico into cabinet archive (makecab.exe)..." -ForegroundColor Cyan
$TempDir = Join-Path $env:TEMP ("revlocal_msi_" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $TempDir | Out-Null

try {
    $CabPath = Join-Path $TempDir "ReverseLocal.cab"
    $DdfPath = Join-Path $TempDir "cabinet.ddf"
    $DdfContent = @"
.OPTION EXPLICIT
.Set CabinetNameTemplate=ReverseLocal.cab
.Set DiskDirectoryTemplate=$TempDir
.Set MaxDiskSize=0
.Set CabinetFileCountThreshold=0
.Set FolderFileCountThreshold=0
.Set FolderSizeThreshold=0
.Set CompressionType=MSZIP
.Set Cabinet=on
.Set Compress=on
"$ExePath" ReverseLocalExe
"$IcoPath" ReverseLocalIco
"@
    Set-Content -Path $DdfPath -Value $DdfContent -Encoding ASCII
    Push-Location $TempDir
    & makecab.exe /F "$DdfPath" | Out-Null
    Pop-Location

    if (-not (Test-Path $CabPath)) {
        throw "Failed to create cabinet file at $CabPath"
    }

    $ExeSize = (Get-Item $ExePath).Length
    $IcoSize = (Get-Item $IcoPath).Length

    Write-Host "[3/4] Building native Windows Installer database ($MsiPath) with embedded Icon via msi.dll..." -ForegroundColor Cyan

    Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;

public static class MsiBuilderV2 {
    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiOpenDatabaseW(string szDatabasePath, IntPtr szPersist, out IntPtr phDatabase);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiDatabaseOpenViewW(IntPtr hDatabase, string szQuery, out IntPtr phView);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiViewExecute(IntPtr hView, IntPtr hRecord);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiViewClose(IntPtr hView);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern IntPtr MsiCreateRecord(uint cParams);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiRecordSetStringW(IntPtr hRecord, uint iField, string szValue);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiRecordSetStreamW(IntPtr hRecord, uint iField, string szFilePath);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiGetSummaryInformationW(IntPtr hDatabase, string szDatabasePath, uint uiUpdateCount, out IntPtr phSummaryInfo);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiSummaryInfoSetPropertyW(IntPtr hSummaryInfo, uint uiProperty, uint uiDataType, int iValue, IntPtr pftValue, string szValue);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiSummaryInfoPersist(IntPtr hSummaryInfo);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiDatabaseCommit(IntPtr hDatabase);

    [DllImport("msi.dll", CharSet = CharSet.Unicode)]
    public static extern uint MsiCloseHandle(IntPtr hAny);

    public static void ExecSql(IntPtr hDb, string sql) {
        IntPtr hView;
        uint res = MsiDatabaseOpenViewW(hDb, sql, out hView);
        if (res != 0) throw new Exception("MsiDatabaseOpenViewW failed (" + res + "): " + sql);
        try {
            res = MsiViewExecute(hView, IntPtr.Zero);
            if (res != 0) throw new Exception("MsiViewExecute failed (" + res + "): " + sql);
            MsiViewClose(hView);
        } finally {
            MsiCloseHandle(hView);
        }
    }

    public static void InsertStreamRow(IntPtr hDb, string tableName, string nameCol, string dataCol, string streamName, string filePath) {
        IntPtr hView;
        string sql = "INSERT INTO `" + tableName + "` (`" + nameCol + "`, `" + dataCol + "`) VALUES (?, ?)";
        uint res = MsiDatabaseOpenViewW(hDb, sql, out hView);
        if (res != 0) throw new Exception("OpenView " + tableName + " failed: " + res);
        IntPtr hRec = MsiCreateRecord(2);
        try {
            MsiRecordSetStringW(hRec, 1, streamName);
            res = MsiRecordSetStreamW(hRec, 2, filePath);
            if (res != 0) throw new Exception("MsiRecordSetStreamW failed: " + res);
            res = MsiViewExecute(hView, hRec);
            if (res != 0) throw new Exception("MsiViewExecute " + tableName + " failed: " + res);
            MsiViewClose(hView);
        } finally {
            MsiCloseHandle(hRec);
            MsiCloseHandle(hView);
        }
    }
}
"@

    $hDb = [IntPtr]::Zero
    $rc = [MsiBuilderV2]::MsiOpenDatabaseW($MsiPath, [IntPtr]4, [ref]$hDb)
    if ($rc -ne 0) { throw "MsiOpenDatabaseW failed with code $rc" }

    try {
        $sqlStatements = @(
            'CREATE TABLE `Property` (`Property` CHAR(72) NOT NULL, `Value` CHAR(0) NOT NULL PRIMARY KEY `Property`)',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ProductCode'', ''{4A8B9F12-6E3D-4B21-9A7C-8D1E2F3A4B5C}'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ProductName'', ''Reverse.Local'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ProductVersion'', ''1.2.0'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''Manufacturer'', ''Hansen Dusenov'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''UpgradeCode'', ''{8F9E4A2D-7B3C-4E1A-9D5F-2C6B8A1E3F40}'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ProductLanguage'', ''1033'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ALLUSERS'', ''1'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ARPNOMODIFY'', ''1'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ARPPRODUCTICON'', ''ArpIcon.ico'')',
            'INSERT INTO `Property` (`Property`, `Value`) VALUES (''ARPURLINFOABOUT'', ''https://reverse.local'')',

            'CREATE TABLE `Icon` (`Name` CHAR(72) NOT NULL, `Data` OBJECT NOT NULL PRIMARY KEY `Name`)',

            'CREATE TABLE `Directory` (`Directory` CHAR(72) NOT NULL, `Directory_Parent` CHAR(72), `DefaultDir` CHAR(255) NOT NULL PRIMARY KEY `Directory`)',
            'INSERT INTO `Directory` (`Directory`, `Directory_Parent`, `DefaultDir`) VALUES (''TARGETDIR'', '''', ''SourceDir'')',
            'INSERT INTO `Directory` (`Directory`, `Directory_Parent`, `DefaultDir`) VALUES (''ProgramFiles64Folder'', ''TARGETDIR'', ''.'')',
            'INSERT INTO `Directory` (`Directory`, `Directory_Parent`, `DefaultDir`) VALUES (''INSTALLDIR'', ''ProgramFiles64Folder'', ''RevLocal|ReverseLocal'')',
            'INSERT INTO `Directory` (`Directory`, `Directory_Parent`, `DefaultDir`) VALUES (''ProgramMenuFolder'', ''TARGETDIR'', ''.'')',
            'INSERT INTO `Directory` (`Directory`, `Directory_Parent`, `DefaultDir`) VALUES (''DesktopFolder'', ''TARGETDIR'', ''.'')',

            'CREATE TABLE `Component` (`Component` CHAR(72) NOT NULL, `ComponentId` CHAR(38), `Directory_` CHAR(72) NOT NULL, `Attributes` SHORT NOT NULL, `Condition` CHAR(255), `KeyPath` CHAR(72) PRIMARY KEY `Component`)',
            'INSERT INTO `Component` (`Component`, `ComponentId`, `Directory_`, `Attributes`, `Condition`, `KeyPath`) VALUES (''MainExeComponent'', ''{3D7A91E0-5A2F-4C9B-8E11-7C3F6A209B14}'', ''INSTALLDIR'', 256, '''', ''ReverseLocalExe'')',

            'CREATE TABLE `Feature` (`Feature` CHAR(38) NOT NULL, `Feature_Parent` CHAR(38), `Title` CHAR(64), `Description` CHAR(255), `Display` SHORT, `Level` SHORT NOT NULL, `Directory_` CHAR(72), `Attributes` SHORT NOT NULL PRIMARY KEY `Feature`)',
            'INSERT INTO `Feature` (`Feature`, `Feature_Parent`, `Title`, `Description`, `Display`, `Level`, `Directory_`, `Attributes`) VALUES (''Complete'', '''', ''Reverse.Local Daemon & Control Panel'', ''Local Reverse Proxy, Native SSH Tunnel & LAN DNS Broadcaster'', 1, 1, ''INSTALLDIR'', 0)',

            'CREATE TABLE `FeatureComponents` (`Feature_` CHAR(38) NOT NULL, `Component_` CHAR(72) NOT NULL PRIMARY KEY `Feature_`, `Component_`)',
            'INSERT INTO `FeatureComponents` (`Feature_`, `Component_`) VALUES (''Complete'', ''MainExeComponent'')',

            'CREATE TABLE `File` (`File` CHAR(72) NOT NULL, `Component_` CHAR(72) NOT NULL, `FileName` CHAR(255) NOT NULL, `FileSize` LONG NOT NULL, `Version` CHAR(72), `Language` CHAR(20), `Attributes` SHORT, `Sequence` SHORT NOT NULL PRIMARY KEY `File`)',
            "INSERT INTO ``File`` (``File``, ``Component_``, ``FileName``, ``FileSize``, ``Version``, ``Language``, ``Attributes``, ``Sequence``) VALUES ('ReverseLocalExe', 'MainExeComponent', 'revlocal.exe|reverse-local.exe', $ExeSize, '1.2.0.0', '1033', 16384, 1)",
            "INSERT INTO ``File`` (``File``, ``Component_``, ``FileName``, ``FileSize``, ``Version``, ``Language``, ``Attributes``, ``Sequence``) VALUES ('ReverseLocalIco', 'MainExeComponent', 'app.ico', $IcoSize, '', '', 16384, 2)",

            'CREATE TABLE `Media` (`DiskId` SHORT NOT NULL, `LastSequence` SHORT NOT NULL, `DiskPrompt` CHAR(64), `Cabinet` CHAR(255), `VolumeLabel` CHAR(32), `Source` CHAR(72) PRIMARY KEY `DiskId`)',
            'INSERT INTO `Media` (`DiskId`, `LastSequence`, `DiskPrompt`, `Cabinet`, `VolumeLabel`, `Source`) VALUES (1, 2, '''', ''#ReverseLocal.cab'', '''', '''')',

            'CREATE TABLE `Shortcut` (`Shortcut` CHAR(72) NOT NULL, `Directory_` CHAR(72) NOT NULL, `Name` CHAR(128) NOT NULL, `Component_` CHAR(72) NOT NULL, `Target` CHAR(72) NOT NULL, `Arguments` CHAR(255), `Description` CHAR(255), `Hotkey` SHORT, `Icon_` CHAR(72), `IconIndex` SHORT, `ShowCmd` SHORT, `WkDir` CHAR(72) PRIMARY KEY `Shortcut`)',
            'INSERT INTO `Shortcut` (`Shortcut`, `Directory_`, `Name`, `Component_`, `Target`, `Arguments`, `Description`, `Hotkey`, `Icon_`, `IconIndex`, `ShowCmd`, `WkDir`) VALUES (''DesktopShortcut'', ''DesktopFolder'', ''RevLocal|Reverse.Local'', ''MainExeComponent'', ''Complete'', ''-control'', ''Reverse.Local Control Panel & Proxy Daemon'', 0, ''ArpIcon.ico'', 0, 1, ''INSTALLDIR'')',
            'INSERT INTO `Shortcut` (`Shortcut`, `Directory_`, `Name`, `Component_`, `Target`, `Arguments`, `Description`, `Hotkey`, `Icon_`, `IconIndex`, `ShowCmd`, `WkDir`) VALUES (''StartMenuShortcut'', ''ProgramMenuFolder'', ''RevLocal|Reverse.Local'', ''MainExeComponent'', ''Complete'', ''-control'', ''Reverse.Local Control Panel & Proxy Daemon'', 0, ''ArpIcon.ico'', 0, 1, ''INSTALLDIR'')',

            'CREATE TABLE `InstallExecuteSequence` (`Action` CHAR(72) NOT NULL, `Condition` CHAR(255), `Sequence` SHORT PRIMARY KEY `Action`)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''CostInitialize'', '''', 800)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''FileCost'', '''', 900)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''CostFinalize'', '''', 1000)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''InstallValidate'', '''', 1400)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''InstallInitialize'', '''', 1500)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''ProcessComponents'', '''', 1600)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''UnpublishFeatures'', '''', 1800)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''RemoveShortcuts'', '''', 3200)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''RemoveFiles'', '''', 3500)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''InstallFiles'', '''', 4000)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''CreateShortcuts'', '''', 4500)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''RegisterUser'', '''', 6000)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''RegisterProduct'', '''', 6100)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''PublishFeatures'', '''', 6300)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''PublishProduct'', '''', 6400)',
            'INSERT INTO `InstallExecuteSequence` (`Action`, `Condition`, `Sequence`) VALUES (''InstallFinalize'', '''', 6600)'
        )

        foreach ($sql in $sqlStatements) {
            [MsiBuilderV2]::ExecSql($hDb, $sql)
        }

        # Embed application icon into Icon table AND cabinet into _Streams table
        [MsiBuilderV2]::InsertStreamRow($hDb, "Icon", "Name", "Data", "ArpIcon.ico", $IcoPath)
        [MsiBuilderV2]::InsertStreamRow($hDb, "_Streams", "Name", "Data", "ReverseLocal.cab", $CabPath)

        # Populate SummaryInformation stream
        $hSum = [IntPtr]::Zero
        $rcSum = [MsiBuilderV2]::MsiGetSummaryInformationW($hDb, $null, 20, [ref]$hSum)
        if ($rcSum -ne 0) { throw "MsiGetSummaryInformationW failed: $rcSum" }
        try {
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 2, 30, 0, [IntPtr]::Zero, "Installation Database") | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 3, 30, 0, [IntPtr]::Zero, "Reverse.Local") | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 4, 30, 0, [IntPtr]::Zero, "Hansen Dusenov") | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 6, 30, 0, [IntPtr]::Zero, "Reverse.Local - Local Reverse Proxy, Native SSH Tunnel & LAN DNS Broadcaster") | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 7, 30, 0, [IntPtr]::Zero, "x64;1033") | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 9, 30, 0, [IntPtr]::Zero, "{B1F4E2D0-8A3C-4E12-9F5A-6C2B8D1E3F99}") | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 14, 3, 500, [IntPtr]::Zero, $null) | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoSetPropertyW($hSum, 15, 3, 2, [IntPtr]::Zero, $null) | Out-Null
            [MsiBuilderV2]::MsiSummaryInfoPersist($hSum) | Out-Null
        } finally {
            [MsiBuilderV2]::MsiCloseHandle($hSum) | Out-Null
        }

        $rcCommit = [MsiBuilderV2]::MsiDatabaseCommit($hDb)
        if ($rcCommit -ne 0) { throw "MsiDatabaseCommit failed: $rcCommit" }
    } finally {
        [MsiBuilderV2]::MsiCloseHandle($hDb) | Out-Null
    }

    Write-Host "[4/4] Signing MSI package with Authenticode certificate..." -ForegroundColor Cyan
    Ensure-AuthenticodeSignature -TargetFile $MsiPath
    Write-Host "[OK] Built and signed Windows Installer package: $MsiPath" -ForegroundColor Green
} finally {
    Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue
}
