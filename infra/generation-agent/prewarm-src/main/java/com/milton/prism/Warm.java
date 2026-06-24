package com.milton.prism;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

// Minimal Spring Boot app: forces a real compile against spring-boot-starter-web
// during the image-build pre-warm so the FULL transitive graph (incl. micrometer,
// the maven-compiler/surefire/spring-boot-maven plugins and their deps) is cached.
@SpringBootApplication
public class Warm {
    public static void main(String[] args) {
        SpringApplication.run(Warm.class, args);
    }
}
