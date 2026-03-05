# Compose: keep animation internals that R8 incorrectly strips
-keep class androidx.compose.animation.core.** { *; }
-keep class androidx.compose.ui.graphics.** { *; }

# Compose runtime
-keep class androidx.compose.runtime.** { *; }

# Material3
-keep class androidx.compose.material3.** { *; }

# gomobile / JNI
-keep class bind.** { *; }
-keep class go.** { *; }
