// Consumer that includes an ambiguous basename ("util.h" exists in both
// src/ and lib/) with no dir-relative or root match → suppressed, no edge.
#include "util.h"

void consume() {
    // intentionally empty
}
