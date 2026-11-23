package com.civquest.mod.test;

public final class MinecraftTestBootstrap {
    private static volatile boolean bootstrapped = false;

    private MinecraftTestBootstrap() {
    }

    public static synchronized void ensureBootstrapped() {
        if (bootstrapped) {
            return;
        }
        try {
            net.neoforged.fml.loading.LoadingModList.of(
                    java.util.List.of(), java.util.List.of(), java.util.List.of(), java.util.List.of(), java.util.Map.of()
            );
        } catch (Throwable ignored) {}
        try {
            java.lang.reflect.Field unsafeField = sun.misc.Unsafe.class.getDeclaredField("theUnsafe");
            unsafeField.setAccessible(true);
            sun.misc.Unsafe unsafe = (sun.misc.Unsafe) unsafeField.get(null);

            java.lang.reflect.Field fmlLoaderDist = net.neoforged.fml.loading.FMLLoader.class.getDeclaredField("dist");
            long offset1 = unsafe.staticFieldOffset(fmlLoaderDist);
            unsafe.putObject(net.neoforged.fml.loading.FMLLoader.class, offset1, net.neoforged.api.distmarker.Dist.DEDICATED_SERVER);

            java.lang.reflect.Field fmlEnvDist = net.neoforged.fml.loading.FMLEnvironment.class.getDeclaredField("dist");
            long offset2 = unsafe.staticFieldOffset(fmlEnvDist);
            unsafe.putObject(net.neoforged.fml.loading.FMLEnvironment.class, offset2, net.neoforged.api.distmarker.Dist.DEDICATED_SERVER);
        } catch (Throwable ignored) {}
        try {
            net.minecraft.SharedConstants.tryDetectVersion();
            net.minecraft.server.Bootstrap.bootStrap();
        } catch (Throwable ignored) {}
        try {
            java.lang.reflect.Method unfreeze = net.neoforged.neoforge.registries.BaseMappedRegistry.class.getDeclaredMethod("unfreeze");
            unfreeze.setAccessible(true);
            @SuppressWarnings({"unchecked", "rawtypes"})
            net.minecraft.core.WritableRegistry rootRegistry = (net.minecraft.core.WritableRegistry) net.minecraft.core.registries.BuiltInRegistries.REGISTRY;
            if (!net.minecraft.core.registries.BuiltInRegistries.REGISTRY.containsKey(net.neoforged.neoforge.registries.NeoForgeRegistries.Keys.FLUID_TYPES.location())) {
                unfreeze.invoke(rootRegistry);
                net.minecraft.core.Registry.register(
                        rootRegistry,
                        net.neoforged.neoforge.registries.NeoForgeRegistries.Keys.FLUID_TYPES.location(),
                        net.neoforged.neoforge.registries.NeoForgeRegistries.FLUID_TYPES
                );
                rootRegistry.freeze();
            }
            var fluidTypes = net.neoforged.neoforge.registries.NeoForgeRegistries.FLUID_TYPES;
            if (!fluidTypes.containsKey(net.minecraft.resources.ResourceLocation.withDefaultNamespace("empty"))) {
                unfreeze.invoke(fluidTypes);
                net.minecraft.core.Registry.register(
                        fluidTypes,
                        net.minecraft.resources.ResourceLocation.withDefaultNamespace("empty"),
                        new net.neoforged.neoforge.fluids.FluidType(net.neoforged.neoforge.fluids.FluidType.Properties.create())
                );
                net.minecraft.core.Registry.register(
                        fluidTypes,
                        net.minecraft.resources.ResourceLocation.withDefaultNamespace("water"),
                        new net.neoforged.neoforge.fluids.FluidType(net.neoforged.neoforge.fluids.FluidType.Properties.create())
                );
                net.minecraft.core.Registry.register(
                        fluidTypes,
                        net.minecraft.resources.ResourceLocation.withDefaultNamespace("lava"),
                        new net.neoforged.neoforge.fluids.FluidType(net.neoforged.neoforge.fluids.FluidType.Properties.create())
                );
                fluidTypes.freeze();
            }
            var attributes = net.minecraft.core.registries.BuiltInRegistries.ATTRIBUTE;
            var swimKey = net.minecraft.resources.ResourceLocation.fromNamespaceAndPath("neoforge", "swim_speed");
            if (!attributes.containsKey(swimKey)) {
                unfreeze.invoke(attributes);
                net.minecraft.core.Registry.register(
                        attributes,
                        swimKey,
                        new net.neoforged.neoforge.common.PercentageAttribute("neoforge.swim_speed", 1.0D, 0.0D, 1024.0D).setSyncable(true)
                );
                net.minecraft.core.Registry.register(
                        attributes,
                        net.minecraft.resources.ResourceLocation.fromNamespaceAndPath("neoforge", "nametag_distance"),
                        new net.minecraft.world.entity.ai.attributes.RangedAttribute("neoforge.name_tag_distance", 64.0D, 0.0D, 64.0D).setSyncable(true)
                );
                net.minecraft.core.Registry.register(
                        attributes,
                        net.minecraft.resources.ResourceLocation.fromNamespaceAndPath("neoforge", "creative_flight"),
                        new net.neoforged.neoforge.common.BooleanAttribute("neoforge.creative_flight", false).setSyncable(true)
                );
                attributes.freeze();
            }
        } catch (Throwable ignored) {}
        try {
            java.lang.reflect.Field cachedValueField = net.neoforged.neoforge.common.ModConfigSpec.ConfigValue.class.getDeclaredField("cachedValue");
            cachedValueField.setAccessible(true);
            cachedValueField.set(net.neoforged.neoforge.common.NeoForgeConfig.SERVER.fullBoundingBoxLadders, Boolean.FALSE);
        } catch (Throwable ignored) {}
        bootstrapped = true;
    }
}
