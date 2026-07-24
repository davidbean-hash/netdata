// SPDX-License-Identifier: GPL-3.0-or-later

#ifndef NETDATA_WINDOWS_WMI_GETSYSTEMINFO_H
#define NETDATA_WINDOWS_WMI_GETSYSTEMINFO_H

#include "../../libnetdata.h"

#if defined(OS_WINDOWS)

typedef struct {
    char Model[256];
    char Manufacturer[256];
    bool Populated;
} Win32ComputerSystemInfo;

bool GetWin32ComputerSystemInfo(Win32ComputerSystemInfo *out);

typedef struct {
    char Caption[256];  // human-friendly OS name, e.g. "Microsoft Windows Server 2022 Standard"
    char Version[64];   // e.g. "10.0.20348"
    // Win32_OperatingSystem.ProductType: 1=Workstation, 2=Domain Controller, 3=Server.
    // ProductTypeValid distinguishes "0 was reported" from "property was missing".
    uint32_t ProductType;
    bool ProductTypeValid;
    bool Populated;
} Win32OperatingSystemInfo;

bool GetWin32OperatingSystemInfo(Win32OperatingSystemInfo *out);

#endif

#endif //NETDATA_WINDOWS_WMI_GETSYSTEMINFO_H
