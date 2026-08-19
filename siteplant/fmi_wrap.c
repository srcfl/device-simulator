//go:build cgo && linux && amd64

#include "fmi2.h"

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdarg.h>

static void siteplant_logger(fmi2ComponentEnvironment env, fmi2String inst, fmi2Status status, fmi2String category, fmi2String message, ...) {
	(void)env;
	if (status <= fmi2Warning) {
		return;
	}
	va_list ap;
	va_start(ap, message);
	fprintf(stderr, "siteplant fmi2[%s/%s]: ", inst ? inst : "?", category ? category : "?");
	vfprintf(stderr, message ? message : "", ap);
	fprintf(stderr, "\n");
	va_end(ap);
}

static fmi2CallbackFunctions siteplant_cbs = {
	.logger = siteplant_logger,
	.allocateMemory = calloc,
	.freeMemory = free,
	.stepFinished = NULL,
	.componentEnvironment = NULL,
};

void* siteplant_dlopen(const char* path) {
	return dlopen(path, RTLD_NOW);
}

const char* siteplant_dlerror(void) {
	return dlerror();
}

void siteplant_dlclose(void* h) {
	if (h) {
		dlclose(h);
	}
}

fmi2Component siteplant_instantiate(void* h, const char* name, const char* guid, const char* resources) {
	fmi2InstantiateTYPE fn = (fmi2InstantiateTYPE)dlsym(h, "fmi2Instantiate");
	if (!fn) {
		return NULL;
	}
	return fn(name, fmi2CoSimulation, guid, resources, &siteplant_cbs, fmi2False, fmi2False);
}

void siteplant_free_instance(void* h, fmi2Component c) {
	fmi2FreeInstanceTYPE fn = (fmi2FreeInstanceTYPE)dlsym(h, "fmi2FreeInstance");
	if (fn && c) {
		fn(c);
	}
}

fmi2Status siteplant_setup(void* h, fmi2Component c, double start) {
	fmi2SetupExperimentTYPE fn = (fmi2SetupExperimentTYPE)dlsym(h, "fmi2SetupExperiment");
	if (!fn) {
		return fmi2Fatal;
	}
	return fn(c, fmi2False, 0, start, fmi2False, 0);
}

fmi2Status siteplant_enter_init(void* h, fmi2Component c) {
	fmi2EnterInitializationModeTYPE fn = (fmi2EnterInitializationModeTYPE)dlsym(h, "fmi2EnterInitializationMode");
	if (!fn) {
		return fmi2Fatal;
	}
	return fn(c);
}

fmi2Status siteplant_exit_init(void* h, fmi2Component c) {
	fmi2ExitInitializationModeTYPE fn = (fmi2ExitInitializationModeTYPE)dlsym(h, "fmi2ExitInitializationMode");
	if (!fn) {
		return fmi2Fatal;
	}
	return fn(c);
}

fmi2Status siteplant_dostep(void* h, fmi2Component c, double t, double dt) {
	fmi2DoStepTYPE fn = (fmi2DoStepTYPE)dlsym(h, "fmi2DoStep");
	if (!fn) {
		return fmi2Fatal;
	}
	return fn(c, t, dt, fmi2True);
}

fmi2Status siteplant_set_real(void* h, fmi2Component c, const fmi2ValueReference vr[], size_t n, const fmi2Real v[]) {
	fmi2SetRealTYPE fn = (fmi2SetRealTYPE)dlsym(h, "fmi2SetReal");
	if (!fn) {
		return fmi2Fatal;
	}
	return fn(c, vr, n, v);
}

fmi2Status siteplant_get_real(void* h, fmi2Component c, const fmi2ValueReference vr[], size_t n, fmi2Real v[]) {
	fmi2GetRealTYPE fn = (fmi2GetRealTYPE)dlsym(h, "fmi2GetReal");
	if (!fn) {
		return fmi2Fatal;
	}
	return fn(c, vr, n, v);
}

void siteplant_terminate(void* h, fmi2Component c) {
	fmi2TerminateTYPE fn = (fmi2TerminateTYPE)dlsym(h, "fmi2Terminate");
	if (fn && c) {
		fn(c);
	}
}
