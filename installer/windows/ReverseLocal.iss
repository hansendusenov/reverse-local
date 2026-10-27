; Inno Setup Script for Reverse.Local (Windows x64)
; Compile with Inno Setup 6 (ISCC.exe ReverseLocal.iss)

[Setup]
AppId={{8F9E4A2D-7B3C-4E1A-9D5F-2C6B8A1E3F40}
AppName=Reverse.Local
AppVersion=1.2.0
AppPublisher=Hansen Dusenov
AppPublisherURL=https://reverse.local
DefaultDirName={autopf}\ReverseLocal
DefaultGroupName=Reverse.Local
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\..\dist
OutputBaseFilename=ReverseLocal-Setup-x64
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

[Files]
Source: "..\..\reverse-local.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Reverse.Local Control Panel"; Filename: "{app}\reverse-local.exe"; Parameters: "-control"
Name: "{autodesktop}\Reverse.Local"; Filename: "{app}\reverse-local.exe"; Parameters: "-control"

[Run]
Filename: "netsh"; Parameters: "advfirewall firewall add rule name=""Reverse.Local HTTP/HTTPS Proxy"" dir=in action=allow program=""{app}\reverse-local.exe"" protocol=TCP localport=80,443"; Flags: runhidden
Filename: "netsh"; Parameters: "advfirewall firewall add rule name=""Reverse.Local LAN DNS & mDNS"" dir=in action=allow program=""{app}\reverse-local.exe"" protocol=UDP localport=53,5353"; Flags: runhidden
Filename: "{app}\reverse-local.exe"; Parameters: "-control"; Description: "Launch Reverse.Local Control Panel"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "taskkill"; Parameters: "/F /IM reverse-local.exe"; Flags: runhidden
Filename: "netsh"; Parameters: "advfirewall firewall delete rule name=""Reverse.Local HTTP/HTTPS Proxy"""; Flags: runhidden
Filename: "netsh"; Parameters: "advfirewall firewall delete rule name=""Reverse.Local LAN DNS & mDNS"""; Flags: runhidden

