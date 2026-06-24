// Implementation of the geometry shapes.
#include "geometry/shape.h"
#include <cmath>

namespace geo {

std::string Shape::name() const {
    return "shape";
}

double circleArea(double r) {
    return 3.14159 * r * r;
}

}  // namespace geo
