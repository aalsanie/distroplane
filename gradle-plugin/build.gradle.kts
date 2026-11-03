import org.gradle.api.tasks.testing.Test
import org.gradle.api.tasks.compile.JavaCompile
import org.gradle.plugin.compatibility.compatibility
import org.gradle.testing.jacoco.tasks.JacocoCoverageVerification
import org.gradle.testing.jacoco.tasks.JacocoReport

plugins {
    id("com.gradle.plugin-publish") version "2.2.1"
    jacoco
}

group = "io.github.aalsanie"
version = providers.environmentVariable("DISTROPLANE_GRADLE_PLUGIN_VERSION").orElse("1.0.0-SNAPSHOT").get()

repositories {
    mavenCentral()
}

gradlePlugin {
    website = "https://github.com/aalsanie/distroplane"
    vcsUrl = "https://github.com/aalsanie/distroplane"
    plugins {
        create("distroplane") {
            id = "io.github.aalsanie.distroplane"
            implementationClass = "io.github.aalsanie.distroplane.gradle.DistroplanePlugin"
            displayName = "Distroplane"
            description = "Explicit, verified native Distroplane release tasks"
            tags = listOf("publishing", "release", "distribution")
            compatibility {
                features {
                    configurationCache = false
                    isolatedProjects = false
                }
            }
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
    jvmArgs(
        "--add-opens=java.base/java.lang=ALL-UNNAMED",
        "--add-opens=java.base/java.util=ALL-UNNAMED",
        "--add-opens=java.base/java.lang.invoke=ALL-UNNAMED",
        "--add-opens=java.base/java.net=ALL-UNNAMED",
    )
}

jacoco {
    toolVersion = "0.8.13"
}

tasks.named<JacocoReport>("jacocoTestReport") {
    dependsOn(tasks.named("test"))
    reports.xml.required.set(true)
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
    dependsOn(tasks.named("jacocoTestCoverageVerification"), tasks.named("jacocoTestReport"))
}
