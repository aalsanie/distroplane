import org.gradle.api.tasks.testing.Test
import org.gradle.api.tasks.compile.JavaCompile
import org.gradle.testing.jacoco.tasks.JacocoCoverageVerification

plugins {
    `java-gradle-plugin`
    jacoco
}

group = "io.github.aalsanie"
version = "1.0.0-SNAPSHOT"

repositories {
    mavenCentral()
}

gradlePlugin {
    plugins {
        create("distroplane") {
            id = "io.github.aalsanie.distroplane"
            implementationClass = "io.github.aalsanie.distroplane.gradle.DistroplanePlugin"
            displayName = "Distroplane"
            description = "Explicit, verified native Distroplane release tasks"
        }
    }
}

dependencies {
    implementation("com.fasterxml.jackson.core:jackson-databind:2.18.12")
    implementation("org.apache.commons:commons-compress:1.28.0")
    testImplementation("org.junit.jupiter:junit-jupiter:5.12.2")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher:1.12.2")
}

tasks.withType<JavaCompile>().configureEach {
    options.release.set(17)
}

tasks.withType<Test>().configureEach {
    useJUnitPlatform()
    maxParallelForks = 1
}

jacoco {
    toolVersion = "0.8.13"
}

tasks.named<JacocoCoverageVerification>("jacocoTestCoverageVerification") {
    dependsOn(tasks.named("test"))
    violationRules {
        rule {
            element = "BUNDLE"
            limit {
                counter = "LINE"
                value = "COVEREDRATIO"
                minimum = "0.9001".toBigDecimal()
            }
            limit {
                counter = "BRANCH"
                value = "COVEREDRATIO"
                minimum = "0.9001".toBigDecimal()
            }
        }
    }
}

tasks.named("check") {
    dependsOn(tasks.named("jacocoTestCoverageVerification"))
}
