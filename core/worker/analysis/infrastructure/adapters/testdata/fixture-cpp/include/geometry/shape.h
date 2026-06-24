// Shape hierarchy for the geometry module.
#ifndef GEOMETRY_SHAPE_H
#define GEOMETRY_SHAPE_H

#include <string>

namespace geo {

class Shape {
public:
    virtual double area() const = 0;
    std::string name() const;
};

struct Point {
    double x;
    double y;
};

enum Kind { CIRCLE, SQUARE };

}  // namespace geo

#endif
