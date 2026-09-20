$features = @('VirtualMachinePlatform','HypervisorPlatform','Microsoft-Hyper-V-All')
foreach ($f in $features) {
  "--- $f ---"
  Get-WindowsOptionalFeature -Online -FeatureName $f -ErrorAction SilentlyContinue | Format-List
}
"--- systeminfo (Hyper-V Requirements) ---"
systeminfo 2>&1 | Select-String -Pattern 'Hyper-V|hyper|virt'
"--- vtx supported ---"
$processor = Get-CimInstance Win32_Processor
"Manufacturer: $($processor.Manufacturer)"
"VM Monitor Mode Extensions: $($processor.VMMonitorModeExtensions)"
"Virtualization Firmware Enabled: $($processor.VirtualizationFirmwareEnabled)"
