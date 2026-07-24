// SPDX-License-Identifier: GPL-3.0-or-later

#ifndef NETDATA_RRDFUNCTIONS_EXPORTERS_H
#define NETDATA_RRDFUNCTIONS_EXPORTERS_H

#include "rrd.h"

#define RRDFUNCTIONS_VERSION_SEPARATOR "|"

void stream_sender_send_global_rrdhost_functions(RRDHOST *host, BUFFER *wb, bool dyncfg);

void host_functions_to_dict(RRDHOST *host, DICTIONARY *dst, void *value, size_t value_size, STRING **help, STRING **tags,
                            HTTP_ACCESS *access, int *priority, uint32_t *version);
void host_functions2json(RRDHOST *host, BUFFER *wb);

#endif //NETDATA_RRDFUNCTIONS_EXPORTERS_H
