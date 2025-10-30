#code example

#external_config.cfg
tool "C++" {
    tool: g++
    flags: -fPIC -Wall -Wextra -std=c++17 -Wconversion -Werror
    include: {/usr/include}
    libdir: {/usr/lib}
    libs: {m, dl, pthread, stdc++}
}

tool "C" {
    tool: gcc
    flags: -std=c11 -fPIC -Wall -Wextra -Wconversion -Werror
    include: {/usr/include}
    libdir: {/usr/lib}
    libs: {m, dl, pthread, stdc++}
}

config "glfw3" {
    include: {/usr/include/glfw3}
    libs: {glfw}
}

config "Vulkan"{
    include: {usr/include/vulkan}
    libs: {vulkan}
}


#main buildy.cfg
workspace "buildy" {

    // configs
    load_config("c++.cfg") //loads settings in config object called C++
    load_config("spirv.cfg") //loads settings in config object called C++
        

    //projects
    project "vulkease" {
        type: shared_library
        sources: { src/*.cpp, src/*.c}
        output: bin/${LIB_NAME}
        config: C++
        config: glfw
        config: vulkan

        
    }

    laod_path("plugins")  //searches for buildy files

}
