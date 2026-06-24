// Util implementation; pairs with util.h in the same directory.
#include "util.h"

static int g_counter = 0;

int helperCount() {
    return ++g_counter;
}
