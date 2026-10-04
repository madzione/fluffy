plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "dev.fluffy.todo_module"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "dev.fluffy.todo_module"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        // Uses the version code from pubspec.yaml. When using split APKs, 1000 * ABI_VERSION
        // is added automatically by Flutter. (https://developer.android.com/studio/build/configure-apk-splits#configure-APK-versions)
        // You can force using the value of versionCode by specifying the `-P force-version-code-ignoring-abi=true`
        // flag during build.
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    buildTypes {
        release {
            // TODO: Add your own signing config for the release build.
            // Signing with the debug keys for now, so `flutter run --release` works.
            signingConfig = signingConfigs.getByName("debug")
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

// fluffy:begin
// Builds the Go library in golang/ into src/main/jniLibs before every build,
// for the ABIs flutter builds (only the device's on flutter run) and with the
// app's NDK.
val fluffyBuildGo = tasks.register<Exec>("fluffyBuildGo") {
    workingDir = rootDir.parentFile
    val abis = (project.findProperty("target-platform") ?: project.findProperty("android.injected.build.abi") ?: "").toString()
    commandLine("fluffy", "lib", "android", "--arch", abis.replace("android-", ""), "--ndk-version", android.ndkVersion ?: "")
    doFirst {
        val onPath = System.getenv("PATH").orEmpty().split(File.pathSeparator)
            .any { File(it, "fluffy").canExecute() || File(it, "fluffy.exe").canExecute() }
        if (!onPath) throw GradleException("fluffy was not found on PATH. The Go library of this app is built by the fluffy CLI during the Flutter build. Install it with \"go install github.com/madzione/fluffy@latest\" and make sure its directory (usually \$(go env GOPATH)/bin, e.g. ~/go/bin) is on PATH, then build again.")
    }
}
tasks.named("preBuild") { dependsOn(fluffyBuildGo) }
// fluffy:end
