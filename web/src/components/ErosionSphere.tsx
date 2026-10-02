import { useEffect, useRef } from 'react'
import * as THREE from 'three'

// A point-cloud sphere eroded by recursive (domain-warped) noise, with a
// comet of larger particles tracing its surface. Two looks: 'light' for the
// cream hero (normal blending, deep ember), 'dark' for dark sections
// (additive glow, as in the reference).

const NOISE = /* glsl */ `
vec3 mod289(vec3 x){return x-floor(x*(1.0/289.0))*289.0;}
vec4 mod289(vec4 x){return x-floor(x*(1.0/289.0))*289.0;}
vec4 permute(vec4 x){return mod289(((x*34.0)+1.0)*x);}
vec4 taylorInvSqrt(vec4 r){return 1.79284291400159-0.85373472095314*r;}
float snoise(vec3 v){
  const vec2 C=vec2(1.0/6.0,1.0/3.0); const vec4 D=vec4(0.0,0.5,1.0,2.0);
  vec3 i=floor(v+dot(v,C.yyy)); vec3 x0=v-i+dot(i,C.xxx);
  vec3 g=step(x0.yzx,x0.xyz); vec3 l=1.0-g; vec3 i1=min(g.xyz,l.zxy); vec3 i2=max(g.xyz,l.zxy);
  vec3 x1=x0-i1+C.xxx; vec3 x2=x0-i2+C.yyy; vec3 x3=x0-D.yyy;
  i=mod289(i);
  vec4 p=permute(permute(permute(i.z+vec4(0.0,i1.z,i2.z,1.0))+i.y+vec4(0.0,i1.y,i2.y,1.0))+i.x+vec4(0.0,i1.x,i2.x,1.0));
  float n_=0.142857142857; vec3 ns=n_*D.wyz-D.xzx;
  vec4 j=p-49.0*floor(p*ns.z*ns.z); vec4 x_=floor(j*ns.z); vec4 y_=floor(j-7.0*x_);
  vec4 x=x_*ns.x+ns.yyyy; vec4 y=y_*ns.x+ns.yyyy; vec4 h=1.0-abs(x)-abs(y);
  vec4 b0=vec4(x.xy,y.xy); vec4 b1=vec4(x.zw,y.zw);
  vec4 s0=floor(b0)*2.0+1.0; vec4 s1=floor(b1)*2.0+1.0; vec4 sh=-step(h,vec4(0.0));
  vec4 a0=b0.xzyw+s0.xzyw*sh.xxyy; vec4 a1=b1.xzyw+s1.xzyw*sh.zzww;
  vec3 p0=vec3(a0.xy,h.x); vec3 p1=vec3(a0.zw,h.y); vec3 p2=vec3(a1.xy,h.z); vec3 p3=vec3(a1.zw,h.w);
  vec4 norm=taylorInvSqrt(vec4(dot(p0,p0),dot(p1,p1),dot(p2,p2),dot(p3,p3)));
  p0*=norm.x; p1*=norm.y; p2*=norm.z; p3*=norm.w;
  vec4 m=max(0.6-vec4(dot(x0,x0),dot(x1,x1),dot(x2,x2),dot(x3,x3)),0.0); m=m*m;
  return 42.0*dot(m*m,vec4(dot(p0,x0),dot(p1,x1),dot(p2,x2),dot(p3,x3)));
}
// Recursive erosion: each octave's domain is warped by the one before it.
float erosion(vec3 p, float t){
  float n1=snoise(p*1.5+vec3(0.0,0.0,t*0.10));
  float n2=snoise(p*3.2+n1*0.9+vec3(t*0.07,0.0,0.0));
  float n3=snoise(p*6.8+n2*0.7-vec3(0.0,t*0.05,0.0));
  return n1*0.55+n2*0.30+n3*0.15;
}
`

const shellVert = /* glsl */ `
uniform float uTime; uniform float uSize; uniform float uPR;
attribute float aSeed;
varying float vAlpha; varying float vHeat;
${NOISE}
void main(){
  vec3 p=normalize(position);
  float e=erosion(p,uTime);
  float keep=smoothstep(-0.18,0.05,e);          // negative field = eroded away
  float r=1.0+e*0.10-(1.0-keep)*0.10+sin(uTime*0.8+aSeed*6.28)*0.004;
  vec4 mv=modelViewMatrix*vec4(p*r,1.0);
  gl_Position=projectionMatrix*mv;
  float front=clamp((4.4+mv.z)/2.0,0.0,1.0);   // 1 on the near face, 0 on the far face
  gl_PointSize=uSize*uPR*(0.55+0.9*keep)*(0.8+0.5*smoothstep(0.0,0.4,e))*(3.0/-mv.z);
  vAlpha=keep*mix(0.2,1.0,front);
  vHeat=smoothstep(-0.05,0.45,e);
}`

const shellFrag = /* glsl */ `
uniform vec3 uColA; uniform vec3 uColB; uniform float uGlow;
varying float vAlpha; varying float vHeat;
void main(){
  float d=length(gl_PointCoord-0.5);
  float a=smoothstep(0.5,0.18,d)*vAlpha;
  if(a<0.01) discard;
  vec3 c=mix(uColA,uColB,vHeat);
  gl_FragColor=vec4(c*(1.0+uGlow*vHeat*0.6),a);
}`

const cometVert = /* glsl */ `
uniform float uTime; uniform float uPR; uniform float uSize;
attribute float aIndex; attribute float aJit;
varying float vT;
${NOISE}
void main(){
  float t=uTime*0.055-aIndex*0.0042;
  float theta=t*6.2831853;
  float lat=0.55+0.28*sin(t*9.42);                 // a wandering band near the top
  lat+=(aJit-0.5)*0.09*(0.3+aIndex/48.0);          // the tail frays
  vec3 p=vec3(cos(theta)*cos(lat), sin(lat), sin(theta)*cos(lat));
  float e=erosion(p,uTime);
  vec4 mv=modelViewMatrix*vec4(p*(1.03+e*0.08+(aJit-0.5)*0.02),1.0);
  gl_Position=projectionMatrix*mv;
  vT=aIndex/48.0;
  float front=smoothstep(-3.6,-2.6,mv.z);
  gl_PointSize=uSize*uPR*mix(3.0,0.7,vT)*(0.6+0.8*aJit)*(3.0/-mv.z)*(0.4+0.6*front);
}`

const cometFrag = /* glsl */ `
uniform vec3 uCore; uniform vec3 uHalo;
varying float vT;
void main(){
  float d=length(gl_PointCoord-0.5);
  float core=smoothstep(0.26,0.0,d);
  float halo=smoothstep(0.5,0.22,d);
  float a=halo*(1.0-vT)*0.95;
  if(a<0.01) discard;
  gl_FragColor=vec4(mix(uHalo,uCore,core),a);
}`

export default function ErosionSphere({ variant = 'light', className }: { variant?: 'light' | 'dark'; className?: string }) {
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = host.current
    if (!el) return
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches || document.documentElement.dataset.motion === 'reduce'
    const small = window.innerWidth < 720

    let renderer: THREE.WebGLRenderer
    try {
      renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' })
    } catch {
      el.classList.add('sphere-fallback') // no WebGL: CSS shows a static gradient orb
      return
    }
    const pr = Math.min(window.devicePixelRatio || 1, 2)
    renderer.setPixelRatio(pr)
    renderer.setClearColor(0x000000, 0)
    el.appendChild(renderer.domElement)

    const scene = new THREE.Scene()
    const camera = new THREE.PerspectiveCamera(38, 1, 0.1, 50)
    camera.position.set(0, 0, 3.4)
    const group = new THREE.Group()
    scene.add(group)

    // Fibonacci sphere: even coverage without clumping at the poles.
    const N = small ? 4200 : 8200
    const pos = new Float32Array(N * 3)
    const seed = new Float32Array(N)
    const golden = Math.PI * (3 - Math.sqrt(5))
    for (let i = 0; i < N; i++) {
      const y = 1 - (i / (N - 1)) * 2
      const r = Math.sqrt(1 - y * y)
      const th = golden * i
      pos[i * 3] = Math.cos(th) * r
      pos[i * 3 + 1] = y
      pos[i * 3 + 2] = Math.sin(th) * r
      seed[i] = Math.random()
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('aSeed', new THREE.BufferAttribute(seed, 1))

    const dark = variant === 'dark'
    const shellMat = new THREE.ShaderMaterial({
      vertexShader: shellVert,
      fragmentShader: shellFrag,
      transparent: true,
      depthWrite: false,
      blending: dark ? THREE.AdditiveBlending : THREE.NormalBlending,
      uniforms: {
        uTime: { value: 0 },
        uSize: { value: small ? 4.2 : 3.6 },
        uPR: { value: pr },
        uColA: { value: new THREE.Color(dark ? '#c2410c' : '#c9531b') },
        uColB: { value: new THREE.Color(dark ? '#ff9a4d' : '#f07b33') },
        uGlow: { value: dark ? 1 : 0 },
      },
    })
    const shell = new THREE.Points(g, shellMat)
    group.add(shell)

    const M = 48
    const idx = new Float32Array(M)
    const jit = new Float32Array(M)
    for (let i = 0; i < M; i++) { idx[i] = i; jit[i] = Math.random() }
    const cg = new THREE.BufferGeometry()
    cg.setAttribute('position', new THREE.BufferAttribute(new Float32Array(M * 3), 3))
    cg.setAttribute('aIndex', new THREE.BufferAttribute(idx, 1))
    cg.setAttribute('aJit', new THREE.BufferAttribute(jit, 1))
    const cometMat = new THREE.ShaderMaterial({
      vertexShader: cometVert,
      fragmentShader: cometFrag,
      transparent: true,
      depthWrite: false,
      blending: dark ? THREE.AdditiveBlending : THREE.NormalBlending,
      uniforms: {
        uTime: { value: 0 },
        uPR: { value: pr },
        uSize: { value: small ? 7 : 6.2 },
        uCore: { value: new THREE.Color(dark ? '#fff4e2' : '#fff6ea') },
        uHalo: { value: new THREE.Color(dark ? '#ffb070' : '#f39a4f') },
      },
    })
    const comet = new THREE.Points(cg, cometMat)
    comet.frustumCulled = false
    group.add(comet)
    group.rotation.set(0.35, 0, -0.12)

    const resize = () => {
      const w = el.clientWidth
      const h = el.clientHeight
      renderer.setSize(w, h, false)
      renderer.domElement.style.width = '100%'
      renderer.domElement.style.height = '100%'
      camera.aspect = w / Math.max(h, 1)
      camera.updateProjectionMatrix()
    }
    resize()
    const ro = new ResizeObserver(resize)
    ro.observe(el)

    // Pointer parallax, eased.
    let tx = 0, ty = 0, cx = 0, cy = 0
    const onMove = (e: PointerEvent) => {
      const r = el.getBoundingClientRect()
      tx = ((e.clientX - r.left) / r.width - 0.5) * 0.5
      ty = ((e.clientY - r.top) / r.height - 0.5) * 0.35
    }
    window.addEventListener('pointermove', onMove, { passive: true })

    let visible = true
    const io = new IntersectionObserver(([e]) => {
      visible = e.isIntersecting
      if (visible && !raf && !reduce) loop()
    })
    io.observe(el)

    const clock = new THREE.Clock()
    let t = 8 // start mid-motion so the first frame already looks alive
    let raf = 0
    const frame = () => {
      const dt = Math.min(clock.getDelta(), 0.05)
      t += dt
      shellMat.uniforms.uTime.value = t
      cometMat.uniforms.uTime.value = t
      cx += (tx - cx) * 0.04
      cy += (ty - cy) * 0.04
      group.rotation.y = t * 0.06 + cx
      group.rotation.x = 0.35 + cy
      renderer.render(scene, camera)
    }
    const loop = () => {
      if (!visible || document.hidden) {
        raf = 0
        return
      }
      frame()
      raf = requestAnimationFrame(loop)
    }
    const onVis = () => {
      if (!document.hidden && !raf && !reduce) {
        clock.getDelta()
        loop()
      }
    }
    document.addEventListener('visibilitychange', onVis)
    if (reduce) frame()
    else loop()

    return () => {
      cancelAnimationFrame(raf)
      raf = -1
      ro.disconnect()
      io.disconnect()
      window.removeEventListener('pointermove', onMove)
      document.removeEventListener('visibilitychange', onVis)
      g.dispose(); cg.dispose(); shellMat.dispose(); cometMat.dispose()
      renderer.dispose()
      renderer.domElement.remove()
    }
  }, [variant])

  return <div ref={host} className={`sphere ${className ?? ''}`} aria-hidden="true" />
}
