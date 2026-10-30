package io.github.aalsanie.distroplane.maven;

/** Explicit, unambiguous mapping of a built Maven artifact into Distroplane intent. */
public final class ArtifactMapping {
    private String name;
    private String module;
    private String classifier;
    private String type;

    public void setName(String value) { this.name = value; }
    public void setModule(String value) { this.module = value; }
    public void setClassifier(String value) { this.classifier = value; }
    public void setType(String value) { this.type = value; }
    public String getName() { return name; }
    public String getModule() { return module; }
    public String getClassifier() { return classifier; }
    public String getType() { return type; }
}
